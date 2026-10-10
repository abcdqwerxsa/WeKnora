package tools

// Package tools — kb_get_image.
//
// Lifts one image chunk out of the bound KB into the session sandbox so
// downstream tools can reference its on-disk path.
//
// Design notes:
//   - Input is a chunk_id, NOT a raw URL. The tool resolves the chunk → KB
//     binding first so a chunk from a KB outside the agent's scope cannot
//     be read: a cross-tenant lookup returns the same generic "chunk not
//     found" as a true 404, never a distinguishable error.
//   - Bytes are pulled via the resolved FileService, never via plain HTTP —
//     provider:// URLs (local://, minio://, s3://, cos://, ...) are how
//     the KB stores them, and the HTTP fallback in image_multimodal.go is
//     the very pattern this tool avoids.
//   - Idempotent path under /workspace/output/images/<kb_id>/<chunk_id>.<ext>.
//     A second call with the same chunk_id that finds an existing file of
//     the same ext returns the cached path without re-downloading.
//   - Byte cap: 10 MiB. A high-DPI A4 scan is ~5 MiB, a 4K photo is
//     ~20 MiB; 10 MiB is the midpoint that keeps the path safely above
//     the 256 KiB per-call model message cap and refuses images that
//     would dominate the sandbox.
//   - The byte-fetch + stage pipeline itself lives in imagefetch so the
//     sibling wiki_get_image tool can reuse it.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/tools/imagefetch"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

// kbImageOutputRoot is re-exported for legacy callers. The imagefetch package
// owns the canonical constant.
const kbImageOutputRoot = imagefetch.OutputRoot

// maxKBImageBytes is the per-call byte cap. The imagefetch package owns the
// canonical constant; this alias keeps the historical name so existing
// tests and any future caller continue to work.
const maxKBImageBytes = imagefetch.MaxBytes

// imageByURLBucket is the synthetic "kb" bucket used when the agent
// passed a raw resource:// / provider:// URL instead of a chunk_id. The
// chunk_id path (imageOutputRoot/<kb>/<chunk>.<ext>) requires a real KB;
// for URL-only inputs we drop the bytes under imageOutputRoot/<this>
// so subsequent calls with the same URL hit the idempotency stat.
const imageByURLBucket = "by_url"

// isImageURL reports whether the input looks like a URL the FileService
// can fetch directly. Used to distinguish "agent passed chunk_id" from
// "agent confused ImageInfo.URL with chunk_id".
func isImageURL(s string) bool {
	return strings.HasPrefix(s, "resource://") ||
		strings.HasPrefix(s, "provider://") ||
		strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "https://")
}

// isModelHandle reports whether the input looks like a request-local
// model handle (e.g. `res://NNNN` or `res::NNNN`) that the modelcontext
// layer should have translated to a canonical URL before the tool
// invocation. Catching this lets us return a specific error instead of
// the generic "chunk not found", which otherwise leaves the agent
// debugging the wrong thing (it would think the chunk is missing when
// the real issue is a stale handle).
func isModelHandle(s string) bool {
	return strings.HasPrefix(s, "res://") || strings.HasPrefix(s, "res::")
}

// isWikiSlug reports whether the input looks like a wiki page slug
// (e.g. `summary/<uuid>` or any `<text>/<uuid>` form). Wiki slugs are
// durable identities that belong to the wiki_read_page tool, not the
// chunk-image pipeline. Catching this lets us point the agent at the
// correct tool instead of letting it chase "chunk not found" forever.
//
// kb_get_image rejects wiki slugs — agents operating on wiki pages
// should use wiki_get_image instead.
func isWikiSlug(s string) bool {
	if !strings.Contains(s, "/") {
		return false
	}
	// `summary/<uuid>` is the canonical wiki summary-page slug. Match
	// the prefix exactly so we don't false-positive on anything else
	// that happens to have a slash + uuid (a file URL, an ImageInfo
	// url that has a path component, etc.).
	if strings.HasPrefix(s, "summary/") {
		return true
	}
	return false
}

// urlToFilenameSlug extracts a filesystem-safe slug from a URL so the
// staged filename stays stable across calls (and idempotency works).
// resource://<handle> → <handle>; provider://<bucket>/<key> → <key's basename>
// (extension stripped to avoid double-suffix when sniff adds its own).
// Anything else (e.g. http URL with no recognisable handle) falls back to
// a 16-char hex hash of the full URL.
func urlToFilenameSlug(rawURL string) string {
	if strings.HasPrefix(rawURL, "resource://") {
		handle := strings.TrimPrefix(rawURL, "resource://")
		handle = strings.SplitN(handle, "?", 2)[0]
		handle = strings.SplitN(handle, "#", 2)[0]
		if handle != "" {
			return handle
		}
	}
	if strings.HasPrefix(rawURL, "provider://") {
		path := strings.TrimPrefix(rawURL, "provider://")
		path = strings.SplitN(path, "?", 2)[0]
		path = strings.SplitN(path, "#", 2)[0]
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			path = path[idx+1:]
		}
		// Strip the extension; detectImageExt will pick one from magic
		// bytes (or fall back to the URL ext) and append it. Leaving the
		// ext here would produce "cert.jpg.jpg".
		if ext := urlExtFromPath(path); ext != "" {
			path = strings.TrimSuffix(path, "."+ext)
		}
		if path != "" {
			return path
		}
	}
	// http(s) or any unrecognized scheme → hash the URL.
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:])[:16]
}

// kbGetImageDescription is the model-facing prompt for the tool. Kept as a
// package-level constant for readability; no per-call metadata is folded in.
const kbGetImageDescription = `Read one image attached to a KB chunk into the session sandbox.

## Usage
- Call after ` + "`knowledge_search`" + ` returns an image-bearing chunk. The
  chunk's ` + "`ImageInfo`" + ` field carries the image URL; ` + "`kb_get_image`" + `
  resolves it back to bytes on the sandbox filesystem.
- ` + "`chunk_id`" + ` accepts both a chunk UUID and an ImageInfo URL
  (` + "`resource://...`" + `, ` + "`provider://...`" + `). The URL path is a
  fallback for the common LLM confusion of "the URL field is the
  chunk_id" — prefer the chunk_id path when you have one, because it lets
  the tool enforce KB-scope safety and parent-chunk inheritance.
- The returned ` + "`path`" + ` is absolute and lives under
  ` + "`/workspace/output/images/<kb_id>/<chunk_id>.<ext>`" + ` (chunk path)
  or ` + "`/workspace/output/images/by_url/<handle>.<ext>`" + ` (URL fallback).
  Reference this path verbatim when downstream tools need the on-disk image.
- Multiple calls with the same ` + "`chunk_id`" + ` (or the same URL) in one
  session are idempotent: the second call returns ` + "`cached: true`" + `
  without re-downloading.

## When to Use
- After ` + "`knowledge_search`" + ` returns image chunks (their result rows
  carry ` + "`ImageInfo`" + `) and the user wants the image embedded into a
  generated document.
- When a downstream tool or skill needs an on-disk path to embed.

## When NOT to Use
- For text content — use ` + "`knowledge_search`" + ` or ` + "`list_knowledge_chunks`" + `.
- For images that are not in a bound KB. The chunk must belong to a KB in
  the agent's scope; chunks from another tenant return the same generic
  "chunk not found" as a true 404.
- For wiki page images. Use ` + "`wiki_get_image(slug, image_index)`" + ` to
  resolve a wiki page slug to its source document's images.

## Path Rules
- The returned path is absolute. Do not edit or move the file before
  the downstream skill reads it.
- The image is stored under ` + "`/workspace/output/`" + ` so the artifact
  collector picks it up for download.

## Size Handling
- 10 MiB cap per call. Larger images are refused with a clear error so
  the caller can resize and retry rather than silently truncating.
- Supported formats detected from magic bytes: PNG, JPEG, GIF, BMP, WebP.
  TIFF and other formats are accepted only when the source URL carries
  an extension as a fallback.

## Returns
- ` + "`{path, size, chunk_id, kb_id, cached, mime, description_path}`" + ` JSON object.
  ` + "`mime`" + ` is the file extension detected from magic bytes or the
  source URL — pass it through when you reference the image so downstream
  tools know the format.

## Sidecar
A sibling ` + "`.txt`" + ` file is staged next to the image (same directory,
same base name, extension swapped for ` + "`.txt`" + `). It carries the source
KB name + id, source document title, chunk id, the chunk's caption / OCR
/ content text, and the original image URL. Read it with
` + "`sandbox_read`" + ` to recover the caption or source-document context
for downstream tools. The tool's response includes ` + "`description_path`" + ` so
you do not need to guess the path. A description write failure does NOT
fail the image stage — the sidecar is an enhancement layer.`

// KBGetImageInput is the input schema for kb_get_image.
type KBGetImageInput struct {
	ChunkID string `json:"chunk_id" jsonschema:"ID of the chunk whose image should be staged. Must be bound to a KB in this agent's scope; use knowledge_search to discover image-bearing chunks. A resource:// or provider:// URL from ImageInfo is also accepted as a fallback when the agent confused URL with chunk_id, but the chunk_id path is preferred because it lets the tool enforce KB-scope safety."`
}

// KBGetImageTool reads one image chunk out of the bound KB into the session
// sandbox, so downstream tools can reference its on-disk path.
type KBGetImageTool struct {
	BaseTool
	fileService          interfaces.FileService
	chunkService         interfaces.ChunkService
	knowledgeService     interfaces.KnowledgeService
	knowledgeBaseService interfaces.KnowledgeBaseService
	storageResolver      interfaces.StorageBackendResolver
	tenantService        interfaces.TenantService
	searchTargets        types.SearchTargets
	fileSink             sandbox.SessionFileStore
}

// NewKBGetImageTool constructs the tool. fileService is the tenant default
// used when the resolver cannot pick a backend-specific one (matches the
// fallback in image_multimodal.go:resolveFileServiceForPayload). chunkService
// enforces tenant isolation on the lookup. knowledgeService and
// knowledgeBaseService power the sidecar description: Knowledge.Title and
// KB.Name are pulled in to give downstream tools a usable caption source.
// Lookups are tenant-scoped (GetKnowledgeBaseByID,
// not ByIDOnly); failures are logged at warn and the corresponding sidecar
// field is left empty rather than failing the stage. storageResolver picks
// the correct backend for a provider:// URL. tenantService supplies the
// *types.Tenant the resolver requires (a nil tenant makes ResolveFileService
// refuse). searchTargets is the agent's resolved KB scope; a chunk belonging
// to a KB not in this set is treated as not-found, never as cross-tenant-
// leak. fileSink MUST NOT be nil — it is where the bytes land.
func NewKBGetImageTool(
	fileService interfaces.FileService,
	chunkService interfaces.ChunkService,
	knowledgeService interfaces.KnowledgeService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	storageResolver interfaces.StorageBackendResolver,
	tenantService interfaces.TenantService,
	searchTargets types.SearchTargets,
	fileSink sandbox.SessionFileStore,
) *KBGetImageTool {
	return &KBGetImageTool{
		BaseTool:             NewBaseTool(ToolKBGetImage, kbGetImageDescription, utils.GenerateSchema[KBGetImageInput]()),
		fileService:          fileService,
		chunkService:         chunkService,
		knowledgeService:     knowledgeService,
		knowledgeBaseService: knowledgeBaseService,
		storageResolver:      storageResolver,
		tenantService:        tenantService,
		searchTargets:        searchTargets,
		fileSink:             fileSink,
	}
}

// Execute pulls the image for the requested chunk and stages it under
// /workspace/output/images/<kb>/<chunk>.<ext>. Returns the absolute path
// plus the image size and a `cached` indicator so downstream tools can
// reference the on-disk image verbatim.
func (t *KBGetImageTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][KBGetImage] Execute started")

	var input KBGetImageInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, nil
	}
	if input.ChunkID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "chunk_id is required",
		}, nil
	}

	sessionID := resolveSessionID(ctx)
	if sessionID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "no session ID in context; kb_get_image must run inside an agent turn",
		}, nil
	}

	// 0. Fallback path: the agent passed an ImageInfo URL (resource://...,
	//    provider://..., http(s)://...) instead of a chunk_id. This is a
	//    common LLM confusion — `knowledge_search` returns chunks whose
	//    image sub-rows carry URLs, and the model sometimes treats the URL
	//    as the handle. Accept the URL directly: skip the chunk lookup,
	//    fetch bytes via the same FileService path, and stage under a
	//    stable /workspace/output/images/by_url/<handle>.<ext> path.
	if isImageURL(input.ChunkID) {
		return t.executeFromURL(ctx, sessionID, input.ChunkID)
	}

	// 0b. Model-handle trap: if the agent passed a request-local handle
	//    like `res::0014`, the modelcontext layer failed to translate it
	//    to a canonical resource:// URL — usually because the handle is
	//    stale or never registered. Surface a specific error so the
	//    agent doesn't waste a turn chasing a non-existent chunk.
	if isModelHandle(input.ChunkID) {
		logger.Warnf(ctx, "[Tool][KBGetImage] unresolved model handle: %s", input.ChunkID)
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"input %q looks like an unresolved model handle (res:// or res:: prefix); "+
					"the modelcontext layer should have translated it to a resource:// URL. "+
					"Either re-issue knowledge_search to get a fresh handle, "+
					"or pass the canonical resource:// URL from ImageInfo.url instead.",
				input.ChunkID),
		}, nil
	}

	// 0c. Wiki-slug trap: `summary/<uuid>` (and similar wiki page
	//    identifiers) is a durable identity that lives in the wiki tree,
	//    not a chunk UUID. The agent picked the wrong tool — kb_get_image
	//    operates on chunks, not wiki pages. Point at the right tool
	//    instead of letting the agent loop on "chunk not found".
	if isWikiSlug(input.ChunkID) {
		logger.Warnf(ctx, "[Tool][KBGetImage] wiki slug passed to chunk tool: %s", input.ChunkID)
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"input %q looks like a wiki page slug (e.g. summary/<uuid>). "+
					"kb_get_image operates on chunk UUIDs only — for wiki page content "+
					"use wiki_read_page, and to embed an image from a wiki page "+
					"use wiki_get_image(slug) which resolves the page's source document.",
				input.ChunkID),
		}, nil
	}

	// 1. Look up the chunk (tenant-scoped via chunkService.GetChunkByID,
	//    which reads tenant from ctx). A 404 and a cross-tenant lookup
	//    return the same generic message — no leak.
	chunk, err := t.chunkService.GetChunkByID(ctx, input.ChunkID)
	if err != nil {
		logger.Debugf(ctx, "[Tool][KBGetImage] chunk lookup failed: chunk=%s err=%v", input.ChunkID, err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("chunk not found: %s", input.ChunkID),
		}, nil
	}

	// 2. Resolve the image URL. image_ocr / image_caption chunks carry no
	//    ImageInfo themselves; they inherit it from the parent text chunk.
	//    `source` is the resolved chunk whose ImageInfo carries the URL;
	//    its Content also feeds the sidecar description (image_caption /
	//    OCR text) downstream tools use as a caption source.
	imageURL, kbID, source, srcErr := t.resolveImageSource(ctx, chunk)
	if srcErr != nil {
		return &types.ToolResult{Success: false, Error: srcErr.Error()}, nil
	}

	// 3. KB scope check. Treat out-of-scope the same as not-found so a
	//    model cannot enumerate chunks by alternating chunk_id inputs.
	if len(t.searchTargets) > 0 && !t.searchTargets.ContainsKB(kbID) {
		logger.Warnf(ctx, "[Tool][KBGetImage] chunk %s belongs to KB %s outside agent scope", input.ChunkID, kbID)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("chunk not found: %s", input.ChunkID),
		}, nil
	}

	// 4. Tenant ID. The chunk already carries one — use it directly so
	//    the resolver picks the right StorageEngineConfig without an
	//    extra round-trip to tenantService. Fall back to ctx if the
	//    chunk row predates the convention.
	tenantID := chunk.TenantID
	if tenantID == 0 {
		tenantID = t.searchTargets.GetTenantIDForKB(kbID)
	}
	if tenantID == 0 {
		if v, ok := types.TenantIDFromContext(ctx); ok {
			tenantID = v
		}
	}

	// 4b. Sidecar metadata. KB / Knowledge lookups are best-effort:
	//     failures are logged at warn and the corresponding sidecar
	//     field is left empty rather than failing the stage. Lookups
	//     happen eagerly here so the closure passed to StageImageBytes
	//     can be a pure render — imagefetch only invokes DescriptionFn
	//     on the fresh-write path, not on every cache-hit probe.
	docTitle, kbName, caption := t.collectKBSidecarFields(ctx, chunk, source, kbID)
	// Content for the sidecar:
	//   - image_ocr / image_caption chunks carry OCR / caption text on
	//     their own Content field; the source (parent) holds the original
	//     surrounding text. Pick the OCR chunk's Content for an image_ocr /
	//     image_caption address so the sidecar captures the image-specific
	//     text downstream tools use as caption / alt text.
	//   - Otherwise (parent text chunk address), source.Content holds the
	//     chunk's text — the natural fit.
	var sidecarContent string
	switch chunk.ChunkType {
	case types.ChunkTypeImageOCR, types.ChunkTypeImageCaption:
		sidecarContent = chunk.Content
	default:
		sidecarContent = source.Content
	}
	descriptionFn := func() (string, error) {
		return renderKBSidecar(kbSidecarFields{
			KBID:          kbID,
			KBName:        kbName,
			KnowledgeID:   chunk.KnowledgeID,
			DocumentTitle: docTitle,
			ChunkID:       input.ChunkID,
			Caption:       caption,
			Content:       sidecarContent,
			ImageURL:      imageURL,
		}), nil
	}

	// 5+. Stage the bytes via the shared helper. Cache path is
	//     /workspace/output/images/<kb>/<chunk>.<ext>.
	res, err := imagefetch.StageImageBytes(ctx, imagefetch.StageRequest{
		SessionID:       sessionID,
		ImageURL:        imageURL,
		KBBucket:        kbID,
		CacheKey:        input.ChunkID,
		TenantID:        tenantID,
		URLHint:         imageURL,
		FileSink:        t.fileSink,
		FileService:     t.fileService,
		StorageResolver: t.storageResolver,
		TenantService:   t.tenantService,
		DescriptionFn:   descriptionFn,
	})
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	logger.Infof(ctx, "[Tool][KBGetImage] staged: chunk=%s kb=%s path=%s bytes=%d desc=%s",
		input.ChunkID, kbID, res.Path, res.Size, res.DescriptionPath)

	return kbGetImageResult(res.Path, res.Size, input.ChunkID, kbID, res.Ext, res.Cached, res.DescriptionPath), nil
}

// resolveImageSource returns the imageURL, kbID, and resolved source chunk
// for a chunk, transparently resolving image_ocr / image_caption chunks via
// their parent. The source chunk is the one whose ImageInfo carries the
// URL; its Content also feeds the sidecar description (image_caption /
// OCR text) downstream tools use as a caption source.
func (t *KBGetImageTool) resolveImageSource(
	ctx context.Context, chunk *types.Chunk,
) (imageURL, kbID string, source *types.Chunk, err error) {
	switch chunk.ChunkType {
	case types.ChunkTypeImageOCR, types.ChunkTypeImageCaption:
		if chunk.ParentChunkID == "" {
			return "", "", nil, fmt.Errorf("image chunk %s has no resolvable parent image", chunk.ID)
		}
		parent, perr := t.chunkService.GetChunkByID(ctx, chunk.ParentChunkID)
		if perr != nil {
			logger.Debugf(ctx, "[Tool][KBGetImage] parent lookup failed: parent=%s err=%v", chunk.ParentChunkID, perr)
			return "", "", nil, fmt.Errorf("image chunk %s has no resolvable parent image", chunk.ID)
		}
		source = parent
	default:
		source = chunk
	}

	infos, perr := parseImageInfos(source.ImageInfo)
	if perr != nil || len(infos) == 0 || infos[0].URL == "" {
		return "", "", nil, fmt.Errorf("chunk %s is not an image chunk — pick a chunk that carries an image", chunk.ID)
	}
	return infos[0].URL, source.KnowledgeBaseID, source, nil
}

// parseImageInfos decodes the JSON-serialized ImageInfo list on a chunk.
// Returns nil without error for the empty case so callers can test len().
func parseImageInfos(raw string) ([]types.ImageInfo, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var infos []types.ImageInfo
	if err := json.Unmarshal([]byte(raw), &infos); err != nil {
		return nil, err
	}
	return infos, nil
}

// urlExtFromPath returns the lowercased file extension of a path/URL, or ""
// when it has none. Used as a pre-flight cache hint so the second call with
// the same chunk_id can skip the download.
func urlExtFromPath(rawURL string) string {
	idx := strings.LastIndex(rawURL, ".")
	if idx < 0 || idx == len(rawURL)-1 {
		return ""
	}
	ext := rawURL[idx+1:]
	// Strip query/fragment that may have leaked in.
	if q := strings.IndexAny(ext, "?#"); q >= 0 {
		ext = ext[:q]
	}
	return strings.ToLower(ext)
}

// kbGetImageResult builds the standard success result for the tool. Used
// for both fresh writes and cache hits so the response shape is identical.
// `descriptionPath` is the sibling .txt sidecar path (empty when the caller
// did not request one or the sidecar write failed). It is included in Data
// so downstream consumers and the agent's follow-up tool calls can
// reference the sidecar verbatim.
func kbGetImageResult(path string, size int64, chunkID, kbID, ext string, cached bool, descriptionPath string) *types.ToolResult {
	var cacheNote string
	if cached {
		cacheNote = " (already staged earlier in this session)"
	}
	output := fmt.Sprintf(
		"Image staged at %s (%d bytes, %s)%s\n\nDescription sidecar: %s\n\n"+
			"Reference this path verbatim when downstream tools need the on-disk image. "+
			"Read the sibling .txt sidecar (description_path above) via sandbox_read for the source document title, "+
			"caption, and chunk content.",
		path, size, ext, cacheNote, descriptionPath,
	)
	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"display_type":     ToolKBGetImage,
			"path":             path,
			"size":             size,
			"chunk_id":         chunkID,
			"kb_id":            kbID,
			"cached":           cached,
			"mime":             ext,
			"description_path": descriptionPath,
		},
	}
}

// Cleanup releases any resources held by the tool. Currently a no-op —
// the FileService and SessionFileStore are owned by the agentService.
func (t *KBGetImageTool) Cleanup(ctx context.Context) error {
	return nil
}

// kbSidecarFields is the bag of values rendered into the KB-path sidecar.
// Empty fields are skipped so a partially-populated sidecar still reads
// cleanly (e.g. when KB lookup fails, kb_name is absent but kb_id is
// still there).
type kbSidecarFields struct {
	KBID          string
	KBName        string
	KnowledgeID   string
	DocumentTitle string
	ChunkID       string
	Caption       string
	Content       string
	ImageURL      string
}

// renderKBSidecar formats the KB sidecar text. Format is stable key: value
// per line so downstream tools (sandbox_read, etc.) can split on newlines
// and parse top-level keys cheaply. Multi-line Content uses a
// YAML block-scalar style ("|" then 2-space indent) so embedded newlines
// survive round-trips through text editors and python file reads.
func renderKBSidecar(f kbSidecarFields) string {
	var b strings.Builder
	fmt.Fprintf(&b, "source: kb\n")
	fmt.Fprintf(&b, "kb_id: %s\n", f.KBID)
	if f.KBName != "" {
		fmt.Fprintf(&b, "kb_name: %s\n", f.KBName)
	}
	fmt.Fprintf(&b, "document_id: %s\n", f.KnowledgeID)
	if f.DocumentTitle != "" {
		fmt.Fprintf(&b, "document_title: %s\n", f.DocumentTitle)
	}
	fmt.Fprintf(&b, "chunk_id: %s\n", f.ChunkID)
	if f.Caption != "" {
		fmt.Fprintf(&b, "caption: %s\n", f.Caption)
	}
	if f.Content != "" {
		fmt.Fprintf(&b, "content: |\n")
		for _, line := range strings.Split(f.Content, "\n") {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	fmt.Fprintf(&b, "image_url: %s\n", f.ImageURL)
	return b.String()
}

// byURLSidecarFields is the bag of values for the URL-fallback sidecar.
// The fallback path has no KB / chunk context — just the URL itself plus
// a note explaining that this image was not fetched from any bound KB.
type byURLSidecarFields struct {
	ImageURL string
}

// renderByURLSidecar formats the URL-fallback sidecar text. Same stable
// key: value convention as the KB sidecar.
func renderByURLSidecar(f byURLSidecarFields) string {
	var b strings.Builder
	fmt.Fprintf(&b, "source: by_url\n")
	fmt.Fprintf(&b, "image_url: %s\n", f.ImageURL)
	fmt.Fprintf(&b, "note: no KB context — this image was fetched from a raw URL, not from any bound KB chunk\n")
	return b.String()
}

// collectKBSidecarFields pulls Knowledge.Title, KB.Name, and the image
// caption best-effort. Each lookup failure is logged at warn and the
// corresponding return value is left empty — callers must not treat
// sidecar metadata as data-integrity-required.
//
// `chunk` is the chunk the agent actually addressed (may be image_ocr /
// image_caption). `source` is the resolved chunk whose ImageInfo carries
// the image URL (returned by resolveImageSource); its ImageInfo may
// also carry the caption. The agent-addressed chunk's own ImageInfo is
// tried as a fallback so a caption set on the OCR/caption chunk itself
// (rather than on the parent text chunk) is still picked up.
func (t *KBGetImageTool) collectKBSidecarFields(
	ctx context.Context,
	chunk *types.Chunk,
	source *types.Chunk,
	kbID string,
) (docTitle, kbName, caption string) {
	if knowledge, kerr := t.knowledgeService.GetKnowledgeByID(ctx, chunk.KnowledgeID); kerr == nil && knowledge != nil {
		docTitle = knowledge.Title
	} else {
		logger.Warnf(ctx, "[Tool][KBGetImage] knowledge lookup failed (sidecar will lack document_title): id=%s err=%v",
			chunk.KnowledgeID, kerr)
	}
	if kb, kberr := t.knowledgeBaseService.GetKnowledgeBaseByID(ctx, kbID); kberr == nil && kb != nil {
		kbName = kb.Name
	} else {
		logger.Warnf(ctx, "[Tool][KBGetImage] KB lookup failed (sidecar will lack kb_name): id=%s err=%v", kbID, kberr)
	}
	if source != nil {
		if infos, perr := parseImageInfos(source.ImageInfo); perr == nil && len(infos) > 0 {
			caption = strings.TrimSpace(infos[0].Caption)
		}
	}
	if caption == "" {
		if infos, perr := parseImageInfos(chunk.ImageInfo); perr == nil && len(infos) > 0 {
			caption = strings.TrimSpace(infos[0].Caption)
		}
	}
	return docTitle, kbName, caption
}

// executeFromURL is the URL-only fallback path taken when the agent
// passed a resource:// or provider:// URL instead of a chunk_id (the
// typical LLM confusion: search results list image URLs alongside chunk
// metadata, and the model grabs the URL field). We bypass the chunk
// lookup entirely and fetch bytes through the same FileService pipeline.
//
// Stage path: /workspace/output/images/by_url/<slug>.<ext>
//   - slug = the resource handle, or the URL hash if unrecognized
//   - ext = magic-byte sniff, falling back to URL extension
//
// KB-scope safety does NOT apply on this path because we never look up
// which KB owns the resource — the URL itself carries no scope info, and
// skipping the chunk lookup means we can't enforce a per-KB check. The
// tenant ID still gates FileService resolution (cross-tenant URLs fail
// to resolve), but a determined agent could pass any URL it has seen in
// the session. That is acceptable for now: this is a fallback for LLM
// ergonomics, not a new authorization surface.
func (t *KBGetImageTool) executeFromURL(
	ctx context.Context,
	sessionID string,
	rawURL string,
) (*types.ToolResult, error) {
	slug := urlToFilenameSlug(rawURL)
	outDir := filepath.Join(kbImageOutputRoot, imageByURLBucket)

	// Idempotency probe: try the URL's extension first, then the common
	// image formats (in case the first call sniffed a different ext).
	// Without this a second call with the same URL would re-download even
	// though the file is sitting on disk under a sniffed ext.
	probeExts := []string{urlExtFromPath(rawURL)}
	probeExts = append(probeExts, "png", "jpg", "gif", "bmp", "webp")
	for _, probeExt := range probeExts {
		if probeExt == "" {
			continue
		}
		probePath := filepath.Join(outDir, slug+"."+probeExt)
		if stat, err := t.fileSink.StatSessionFile(ctx, sessionID, probePath); err == nil &&
			stat != nil && stat.Type == sandbox.RemoteEntryFile {
			logger.Infof(ctx, "[Tool][KBGetImage] cache hit (URL fallback): url=%s path=%s size=%d",
				rawURL, probePath, stat.Size)
			// Probe the sidecar from a previous call so the agent can still
			// read the description if it was already written. Same convention
			// imagefetch.StageImageBytes uses internally on its cache-hit
			// path.
			descPath := filepath.Join(outDir, slug+imagefetch.DescriptionExt)
			var descPathStr string
			if dstat, derr := t.fileSink.StatSessionFile(ctx, sessionID, descPath); derr == nil &&
				dstat != nil && dstat.Type == sandbox.RemoteEntryFile {
				descPathStr = descPath
			}
			return kbGetImageResult(probePath, stat.Size, rawURL, imageByURLBucket, probeExt, true, descPathStr), nil
		}
	}

	tenantID, _ := types.TenantIDFromContext(ctx)
	res, err := imagefetch.StageImageBytes(ctx, imagefetch.StageRequest{
		SessionID:       sessionID,
		ImageURL:        rawURL,
		KBBucket:        imageByURLBucket,
		CacheKey:        slug,
		TenantID:        tenantID,
		URLHint:         rawURL,
		FileSink:        t.fileSink,
		FileService:     t.fileService,
		StorageResolver: t.storageResolver,
		TenantService:   t.tenantService,
		Description: renderByURLSidecar(byURLSidecarFields{
			ImageURL: rawURL,
		}),
	})
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	logger.Infof(ctx, "[Tool][KBGetImage] staged (URL fallback): url=%s path=%s bytes=%d desc=%s",
		rawURL, res.Path, res.Size, res.DescriptionPath)

	return kbGetImageResult(res.Path, res.Size, rawURL, imageByURLBucket, res.Ext, res.Cached, res.DescriptionPath), nil
}
