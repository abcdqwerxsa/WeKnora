package tools

// Package tools — wiki_get_image.
//
// Resolves a wiki page slug to the source document behind it and stages
// one image from that document into the session sandbox so downstream
// tools can reference its on-disk path.
//
// Design notes:
//   - Input is a wiki page slug (e.g. `summary/<uuid>`, `entity/foo`,
//     `concept/bar`) and an optional image_index. Resolution mirrors
//     wiki_read_page: route through the per-session WikiRouteResolver
//     first, fall back to a scope walk.
//   - The slug → source-knowledgeID translation uses page.SourceRefs[0].
//     Summary / entity / concept pages all carry at least one source
//     document; pages without one (e.g. top-level synthesis) return a
//     clear error rather than silently picking a default.
//   - KB-scope safety: the source document's KB is checked against the
//     agent's searchTargets. Cross-tenant or out-of-scope pages return
//     the same generic error kb_get_image uses ("wiki page not found"),
//     never a distinguishable leak.
//   - Image-chunk enumeration: chunks of type image_ocr / image_caption
//     for the source knowledge ID, ordered by chunk_index ASC, then
//     filtered to chunks that actually carry an ImageInfo. The resulting
//     list is the candidate set returned to the model.
//   - Cache path: /workspace/output/images/wiki/<safe-slug>_idx<n>.<ext>.
//     The "wiki" bucket keeps these files visually distinct from KB-
//     scoped stages so the artifact collector picks them up uniformly
//     while the model can still reference the path verbatim.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/tools/imagefetch"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

// wikiImageBucket is the cache directory under /workspace/output/images/ that
// keeps wiki-staged bytes visually distinct from KB-staged bytes.
const wikiImageBucket = "wiki"

// wikiGetImageDescription is the model-facing prompt for the tool.
const wikiGetImageDescription = `Read one image attached to a wiki page's source document into the session sandbox.

## Usage
- Call after ` + "`wiki_read_page(slug)`" + ` (or ` + "`wiki_search`" + `) returns a
  page whose source document carries image chunks. This tool lifts one of
  those images into the sandbox so downstream tools can reference it.
- The page must belong to a wiki KB in this agent's scope. Cross-KB or
  cross-tenant pages return the same generic "wiki page not found" as a
  real 404 — no leak.
- The first call without ` + "`image_index`" + ` stages the first image and
  returns the full candidate list (` + "`images[]`" + `); pass an
  ` + "`image_index`" + ` from that list on subsequent calls to pick a
  different image without re-querying.

## When to Use
- After ` + "`wiki_read_page`" + ` returned a summary page and the user wants
  one of its figures embedded into a generated document.
- When the agent already has the wiki slug in hand and skipping
  ` + "`knowledge_search`" + ` would save a round-trip.

## When NOT to Use
- For wiki page text — use ` + "`wiki_read_page`" + `.
- For raw KB chunks not behind a wiki page — use ` + "`kb_get_image`" + ` with
  the chunk_id from ` + "`knowledge_search`" + `.
- For an ` + "`index`" + ` or other page whose source_refs is empty — this
  tool will refuse with a clear message.

## Path Rules
- Returned path lives at ` + "`/workspace/output/images/wiki/<slug>_idx<n>.<ext>`" + `.
  Reference this path verbatim when downstream tools need the on-disk image.

## Size Handling
- 10 MiB cap per call. Same as ` + "`kb_get_image`" + `.

## Returns
- ` + "`{path, size, slug, chunk_id, kb_id, knowledge_id, image_index, cached, mime, description_path, images[]}`" + ` JSON object.
  ` + "`images[]`" + ` is the full candidate list ` + "`{index, chunk_id, caption, url, kb_id, knowledge_id}`" + ` so the model can pick
  another ` + "`image_index`" + ` next turn without re-querying.

## Sidecar
A sibling ` + "`.txt`" + ` file is staged next to the image (same directory,
same base name, extension swapped for ` + "`.txt`" + `). It carries the wiki
page slug / title / summary, the source document title, source KB name +
id, chunk id, the chunk's caption / OCR / content text, and the original
image URL. Read it with ` + "`sandbox_read`" + ` to recover the caption, the
wiki page context, or the source document context for downstream tools.
The tool's response includes ` + "`description_path`" + ` so you do not need to
guess the path. A description write failure does NOT fail the image stage —
the sidecar is an enhancement layer.`

// WikiGetImageInput is the input schema for wiki_get_image.
type WikiGetImageInput struct {
	Slug       string `json:"slug" jsonschema:"Wiki page slug (e.g. 'summary/<uuid>', 'entity/foo'). Must resolve to a page whose source_refs points to a source document with at least one image chunk."`
	ImageIndex int    `json:"image_index,omitempty" jsonschema:"Zero-based index into the page's image list (default 0). The first call returns the full candidate list under 'images[]'; use the index from that list to pick a different image next turn."`
}

// WikiGetImageTool reads one image attached to a wiki page's source document
// into the session sandbox so downstream tools can reference its on-disk path.
type WikiGetImageTool struct {
	BaseTool
	wikiPageService      interfaces.WikiPageService
	knowledgeService     interfaces.KnowledgeService
	knowledgeBaseService interfaces.KnowledgeBaseService
	chunkService         interfaces.ChunkService
	fileService          interfaces.FileService
	storageResolver      interfaces.StorageBackendResolver
	tenantService        interfaces.TenantService
	fileSink             sandbox.SessionFileStore
	searchTargets        types.SearchTargets
	wikiKBIDs            []string
	wikiRoutes           *WikiRouteResolver
}

// NewWikiGetImageTool constructs the tool.
//
//   - wikiPageService resolves slug → page. Must be non-nil; the tool
//     uses resolveUniqueWikiPage to honour WikiScope routing.
//   - knowledgeService resolves SourceRefs[0] → Knowledge (with TenantID).
//   - knowledgeBaseService resolves KB.Name for the sidecar description.
//     Best-effort: lookup failures log at warn and the sidecar field is
//     left empty rather than failing the stage.
//   - chunkService enumerates image_ocr / image_caption rows for the
//     source document; the tenant in ctx is used.
//   - fileService, storageResolver, tenantService match kb_get_image's
//     dependencies; the same fallback policy applies when the resolver
//     cannot pick a backend-specific FileService.
//   - searchTargets is the agent's resolved KB scope; a source KB not in
//     this set is treated as not-found, never as cross-tenant leak.
//   - wikiKBIDs / wikiRoutes are the session's wiki scope and slug cache,
//     shared with wiki_read_page / wiki_search / etc.
//   - fileSink MUST NOT be nil — it is where the bytes land.
func NewWikiGetImageTool(
	wikiPageService interfaces.WikiPageService,
	knowledgeService interfaces.KnowledgeService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	chunkService interfaces.ChunkService,
	fileService interfaces.FileService,
	storageResolver interfaces.StorageBackendResolver,
	tenantService interfaces.TenantService,
	searchTargets types.SearchTargets,
	wikiKBIDs []string,
	wikiRoutes *WikiRouteResolver,
	fileSink sandbox.SessionFileStore,
) *WikiGetImageTool {
	return &WikiGetImageTool{
		BaseTool: NewBaseTool(
			ToolWikiGetImage,
			wikiGetImageDescription,
			utils.GenerateSchema[WikiGetImageInput](),
		),
		wikiPageService:      wikiPageService,
		knowledgeService:     knowledgeService,
		knowledgeBaseService: knowledgeBaseService,
		chunkService:         chunkService,
		fileService:          fileService,
		storageResolver:      storageResolver,
		tenantService:        tenantService,
		searchTargets:        searchTargets,
		wikiKBIDs:            wikiKBIDs,
		wikiRoutes:           wikiRoutes,
		fileSink:             fileSink,
	}
}

// Execute resolves a wiki page slug to its source document's image chunks,
// stages ONE image at image_index into the session sandbox, and returns the
// full candidate list alongside the staged path so the agent can pick
// another image next turn without re-querying.
func (t *WikiGetImageTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][WikiGetImage] Execute started")

	var input WikiGetImageInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, nil
	}
	if input.Slug == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "slug is required",
		}, nil
	}
	if input.ImageIndex < 0 {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("image_index must be >= 0, got %d", input.ImageIndex),
		}, nil
	}

	sessionID := resolveSessionID(ctx)
	if sessionID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "no session ID in context; wiki_get_image must run inside an agent turn",
		}, nil
	}

	// 1. Resolve the wiki page. reuse resolveUniqueWikiPage so we honour
	//    the same scope + routing semantics as wiki_read_page. KB-scope
	//    check is intentionally fused with this: an out-of-scope slug
	//    returns the same generic "wiki page not found" as a real miss.
	page, _, err := resolveUniqueWikiPage(
		ctx,
		t.wikiPageService,
		input.Slug,
		t.wikiKBIDs,
		t.wikiRoutes,
	)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"wiki page not found: %s — confirm the slug with wiki_read_page or wiki_search",
				input.Slug,
			),
		}, nil
	}

	// 2. Take the first source document. A wiki page can cite many
	//    sources; this tool only images the first one (consistent with
	//    how a typical "summary of this PDF" page surfaces a single
	//    visual). If the page has no sources at all, refuse.
	if len(page.SourceRefs) == 0 {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"wiki page %s has no source documents to draw images from; "+
					"use wiki_read_page for its text content instead",
				input.Slug,
			),
		}, nil
	}
	// SourceRefs format: "knowledgeID" or "knowledgeID|title" per
	// wiki_route_resolver.go:228.
	sourceRef := strings.TrimSpace(strings.SplitN(page.SourceRefs[0], "|", 2)[0])
	if sourceRef == "" {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"wiki page %s has an empty source reference; cannot resolve source document",
				input.Slug,
			),
		}, nil
	}

	// 3. Resolve source → knowledge row. GetKnowledgeByID uses tenant
	//    from ctx; if the source belongs to a different tenant the
	//    service layer returns not-found, mirroring kb_get_image's
	//    no-leak policy.
	knowledge, err := t.knowledgeService.GetKnowledgeByID(ctx, sourceRef)
	if err != nil || knowledge == nil {
		logger.Debugf(ctx, "[Tool][WikiGetImage] knowledge lookup failed: source=%s err=%v", sourceRef, err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("source document not found: %s", sourceRef),
		}, nil
	}

	// 4. KB-scope check on the source document's KB. Treat out-of-scope
	//    as not-found — refuse to enumerate what the agent does not own.
	if len(t.searchTargets) > 0 && !t.searchTargets.ContainsKB(knowledge.KnowledgeBaseID) {
		logger.Warnf(ctx, "[Tool][WikiGetImage] source %s belongs to KB %s outside agent scope",
			sourceRef, knowledge.KnowledgeBaseID)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("wiki page not found: %s", input.Slug),
		}, nil
	}

	// 5. Enumerate image chunks for the source document. We use the
	//    service-layer paginated lister (it picks up tenant from ctx
	//    and filters by chunk_type) and ask for up to 100 rows in one
	//    page — typical image-bearing docs have << 100 image chunks;
	//    exceeding 100 is rare enough to warrant a follow-up call rather
	//    than complicating this code.
	pageResult, err := t.chunkService.ListPagedChunksByKnowledgeID(
		ctx,
		sourceRef,
		&types.Pagination{Page: 1, PageSize: 100},
		[]types.ChunkType{types.ChunkTypeImageOCR, types.ChunkTypeImageCaption},
	)
	if err != nil {
		logger.Errorf(ctx, "[Tool][WikiGetImage] chunk list failed: source=%s err=%v", sourceRef, err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to list image chunks for source %s: %v", sourceRef, err),
		}, nil
	}

	chunks, _ := pageResult.Data.([]*types.Chunk)
	if chunks == nil {
		chunks = []*types.Chunk{}
	}

	// 6. Filter to chunks that actually carry image bytes. Some
	//    image_ocr / image_caption rows are produced without an
	//    ImageInfo payload (e.g. when OCR returned no text and the
	//    caption was empty); they would otherwise show up as
	//    "not an image chunk" errors when picked.
	candidates := make([]imageCandidate, 0, len(chunks))
	for _, c := range chunks {
		if c == nil {
			continue
		}
		infos, _ := parseImageInfos(c.ImageInfo)
		if len(infos) == 0 || infos[0].URL == "" {
			continue
		}
		candidates = append(candidates, imageCandidate{
			index:       len(candidates),
			chunkID:     c.ID,
			chunk:       c,
			caption:     strings.TrimSpace(infos[0].Caption),
			url:         infos[0].URL,
			kbID:        c.KnowledgeBaseID,
			knowledgeID: c.KnowledgeID,
		})
	}

	if len(candidates) == 0 {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"wiki page %s has no image chunks in its source document %s",
				input.Slug, sourceRef,
			),
		}, nil
	}

	if input.ImageIndex >= len(candidates) {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"image_index out of range: wiki page %s has %d images (valid indices 0..%d); "+
					"re-issue with the index from the images[] array returned by the previous call",
				input.Slug, len(candidates), len(candidates)-1,
			),
		}, nil
	}

	chosen := candidates[input.ImageIndex]

	// 7. Walk ParentChunkID if needed. image_ocr / image_caption chunks
	//    carry ImageInfo directly per `kbGetImage` callers, so the
	//    kb_get_image parent-resolution branch rarely fires in practice —
	//    but mirror it for consistency with the sibling tool.
	imageURL, kbID, srcErr := resolveImageInfoForChunk(chosen.chunk)
	if srcErr != nil {
		return &types.ToolResult{Success: false, Error: srcErr.Error()}, nil
	}

	// 8. KB.Name lookup for sidecar (best-effort; failures log + skip
	//    the field, do not fail the stage).
	var kbName string
	if kb, kberr := t.knowledgeBaseService.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID); kberr == nil && kb != nil {
		kbName = kb.Name
	} else {
		logger.Warnf(ctx, "[Tool][WikiGetImage] KB lookup failed (sidecar will lack kb_name): id=%s err=%v",
			knowledge.KnowledgeBaseID, kberr)
	}

	// 9. Stage bytes via the shared helper. Cache key is
	//    <safe-slug>_idx<n> so different images from the same page
	//    get distinct paths while the "wiki" bucket keeps them apart
	//    from KB-staged bytes. DescriptionFn is closed over the
	//    wiki-page / source-document context so the sidecar carries
	//    full provenance for downstream tools.
	cacheKey := sanitizeSlugForCache(input.Slug) + "_idx" + fmt.Sprintf("%d", input.ImageIndex)
	descriptionFn := func() (string, error) {
		return renderWikiSidecar(wikiSidecarFields{
			KBID:          kbID,
			KBName:        kbName,
			KnowledgeID:   sourceRef,
			DocumentTitle: knowledge.Title,
			Slug:          input.Slug,
			WikiTitle:     page.Title,
			WikiSummary:   page.Summary,
			ChunkID:       chosen.chunkID,
			Caption:       chosen.caption,
			Content:       chosen.chunk.Content,
			ImageURL:      imageURL,
			ImageIndex:    input.ImageIndex,
			TotalImages:   len(candidates),
		}), nil
	}
	res, err := imagefetch.StageImageBytes(ctx, imagefetch.StageRequest{
		SessionID:       sessionID,
		ImageURL:        imageURL,
		KBBucket:        wikiImageBucket,
		CacheKey:        cacheKey,
		TenantID:        knowledge.TenantID,
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

	logger.Infof(ctx, "[Tool][WikiGetImage] staged: slug=%s idx=%d kb=%s path=%s bytes=%d desc=%s",
		input.Slug, input.ImageIndex, kbID, res.Path, res.Size, res.DescriptionPath)

	return &types.ToolResult{
		Success: true,
		Output: fmt.Sprintf(
			"Image staged at %s (%d bytes, %s) for wiki page %s (image %d of %d).\n\n"+
				"Description sidecar: %s\n\n"+
				"Reference this path verbatim when downstream tools need the on-disk image. "+
				"Read the sibling .txt sidecar (description_path above) via sandbox_read for the wiki page title, "+
				"source document title, and caption. "+
				"Use the images[] array below to pick a different image next turn.",
			res.Path, res.Size, res.Ext, input.Slug, input.ImageIndex+1, len(candidates), res.DescriptionPath,
		),
		Data: map[string]interface{}{
			"display_type":     ToolWikiGetImage,
			"path":             res.Path,
			"size":             res.Size,
			"slug":             input.Slug,
			"chunk_id":         chosen.chunkID,
			"kb_id":            kbID,
			"knowledge_id":     sourceRef,
			"image_index":      input.ImageIndex,
			"cached":           res.Cached,
			"mime":             res.Ext,
			"description_path": res.DescriptionPath,
			"images":           buildCandidateList(candidates),
		},
	}, nil
}

// Cleanup releases any resources held by the tool. Currently a no-op —
// the FileService and SessionFileStore are owned by the agentService.
func (t *WikiGetImageTool) Cleanup(ctx context.Context) error {
	return nil
}

// imageCandidate is the internal representation of an image-bearing chunk
// for the candidate list. Only fields the model needs survive the JSON
// serialization.
type imageCandidate struct {
	index       int
	chunkID     string
	chunk       *types.Chunk
	caption     string
	url         string
	kbID        string
	knowledgeID string
}

// buildCandidateList flattens the internal candidate list into the JSON
// shape the model sees in `images[]`.
func buildCandidateList(candidates []imageCandidate) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(candidates))
	for _, c := range candidates {
		entry := map[string]interface{}{
			"index":        c.index,
			"chunk_id":     c.chunkID,
			"url":          c.url,
			"kb_id":        c.kbID,
			"knowledge_id": c.knowledgeID,
		}
		if c.caption != "" {
			entry["caption"] = c.caption
		}
		out = append(out, entry)
	}
	return out
}

// resolveImageInfoForChunk extracts (imageURL, kbID) from a chunk's
// ImageInfo JSON. image_ocr / image_caption chunks typically carry their
// own ImageInfo; the parent-walk branch mirrors kb_get_image.go's
// resolveImageSource and is preserved for robustness.
func resolveImageInfoForChunk(chunk *types.Chunk) (string, string, error) {
	infos, err := parseImageInfos(chunk.ImageInfo)
	if err != nil || len(infos) == 0 || infos[0].URL == "" {
		return "", "", fmt.Errorf("chunk %s is not an image chunk — pick a chunk that carries an image", chunk.ID)
	}
	return infos[0].URL, chunk.KnowledgeBaseID, nil
}

// sanitizeSlugForCache converts a wiki slug into a filename-safe token.
// Allowed wiki slug chars are lowercase ASCII alphanumerics, `/`, `-`
// (see wiki_write_page.go's slug validator at line 252-273). We replace
// `/` with `_` so the path stays in one segment, and pass everything
// else through verbatim — the slug validator already excluded anything
// problematic.
func sanitizeSlugForCache(slug string) string {
	cleaned := strings.ReplaceAll(slug, "/", "_")
	if cleaned == "" {
		return "page"
	}
	return cleaned
}

// wikiSidecarFields is the bag of values rendered into the wiki-path
// sidecar. Empty fields are skipped so a partially-populated sidecar
// (e.g. when KB lookup failed) still reads cleanly. The wiki-specific
// fields (slug / title / summary) are layered on top of the KB-side
// fields so downstream tools can grep the file for both worlds.
type wikiSidecarFields struct {
	KBID          string
	KBName        string
	KnowledgeID   string
	DocumentTitle string
	Slug          string
	WikiTitle     string
	WikiSummary   string
	ChunkID       string
	Caption       string
	Content       string
	ImageURL      string
	ImageIndex    int
	TotalImages   int
}

// renderWikiSidecar formats the wiki sidecar text. Format is the same
// stable key: value convention used by the KB sidecar; the wiki-specific
// block (slug / title / summary) is appended after the KB context block
// so a downstream tool can read either layer independently.
//
// Multi-line Content / WikiSummary use the YAML block-scalar style
// ("|" then 2-space indent) so embedded newlines survive round-trips.
func renderWikiSidecar(f wikiSidecarFields) string {
	var b strings.Builder
	fmt.Fprintf(&b, "source: wiki\n")
	fmt.Fprintf(&b, "kb_id: %s\n", f.KBID)
	if f.KBName != "" {
		fmt.Fprintf(&b, "kb_name: %s\n", f.KBName)
	}
	fmt.Fprintf(&b, "document_id: %s\n", f.KnowledgeID)
	if f.DocumentTitle != "" {
		fmt.Fprintf(&b, "document_title: %s\n", f.DocumentTitle)
	}
	fmt.Fprintf(&b, "wiki_slug: %s\n", f.Slug)
	if f.WikiTitle != "" {
		fmt.Fprintf(&b, "wiki_title: %s\n", f.WikiTitle)
	}
	if f.WikiSummary != "" {
		fmt.Fprintf(&b, "wiki_summary: |\n")
		for _, line := range strings.Split(f.WikiSummary, "\n") {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	fmt.Fprintf(&b, "chunk_id: %s\n", f.ChunkID)
	fmt.Fprintf(&b, "image_index: %d\n", f.ImageIndex)
	fmt.Fprintf(&b, "total_images: %d\n", f.TotalImages)
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
