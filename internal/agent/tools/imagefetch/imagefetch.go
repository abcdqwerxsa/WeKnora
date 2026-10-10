// Package imagefetch centralizes the byte-fetch + stage pipeline used by the
// kb_get_image and wiki_get_image tools.
//
// Both tools translate their input (a chunk UUID or a wiki page slug) into
// (imageURL, tenantID, kbBucket, cacheKey) and then call StageImageBytes to
// fetch bytes, sniff the format, enforce the 10 MiB cap, and write the result
// into the session workspace. The session workspace path is idempotent so a
// second call with the same cache key returns the same path without
// re-downloading.
//
// Keeping this logic in a shared package avoids two near-identical copies of
// the resolveFileService / GetFile / size cap / MIME sniff / stage pipeline
// drifting apart across tools.
package imagefetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// OutputRoot is the directory under /workspace/output where both kb_get_image
// and wiki_get_image stage bytes. Living under the artifact collector's
// SessionOutputRoot means downstream tools can reference the staged path
// directly in any artifact they produce.
const OutputRoot = "/workspace/output/images"

// MaxBytes bounds one image fetch. 10 MiB is the midpoint between a 5 MiB
// high-DPI A4 scan and a 20 MiB 4K photo: large enough for typical doc
// figures, small enough to refuse images that would dominate the sandbox.
const MaxBytes = 10 << 20

// DescriptionExt is the sidecar file extension used by kb_get_image and
// wiki_get_image. Convention: for an image staged at
// /workspace/output/images/<bucket>/<key>.<ext>, the sidecar lives at
// the same directory with the same base name and this extension. Read
// via sandbox_read to recover caption / source document title / chunk
// content for downstream tools.
const DescriptionExt = ".txt"

// StageRequest is the resolved request to fetch+stage a single image.
// Both kb_get_image and wiki_get_image prepare this from their respective
// input translations and pass it to StageImageBytes.
//
//   - SessionID is required; the session workspace is the destination.
//   - ImageURL is the canonical URL to fetch (resource://, provider://, http(s)).
//   - KBBucket partitions the cache directory; kb_get_image uses the source
//     chunk's KB ID, wiki_get_image uses the literal "wiki", and the URL-
//     fallback path uses "by_url". Two different buckets under the same
//     cache key never collide.
//   - CacheKey is the deterministic filename within KBBucket (without
//     extension); the extension is chosen from magic-byte sniff with a
//     URL-extension fallback.
//   - TenantID, when non-zero, is used to pick the right FileService via the
//     storage resolver. A zero value falls back to ctx + default file service.
//   - FileSink, FileService, StorageResolver, TenantService are the standard
//     dependencies; nil-tolerant behavior matches kb_get_image's existing
//     fallback policy (resolver failure → default file service).
type StageRequest struct {
	SessionID       string
	ImageURL        string
	KBBucket        string
	CacheKey        string
	TenantID        uint64
	URLHint         string // optional URL to derive a pre-sniff hint ext from
	FileSink        sandbox.SessionFileStore
	FileService     interfaces.FileService
	StorageResolver interfaces.StorageBackendResolver
	TenantService   interfaces.TenantService
	// Description is the optional sidecar text. Both Description and
	// DescriptionFn are optional; if both are empty, no sidecar is written
	// and StageResult.DescriptionPath stays empty. Prefer DescriptionFn when
	// the sidecar content requires lookups (KB / Knowledge / WikiPage) so
	// the cost is only paid when an image is actually fetched, not on every
	// cache-hit probe.
	Description   string
	DescriptionFn func() (string, error)
}

// StageResult describes what landed on disk. The caller wraps these into the
// tool-specific ToolResult.Data shape.
type StageResult struct {
	Path   string // absolute path under /workspace/output/images/<bucket>/<key>.<ext>
	Size   int64
	Ext    string
	Cached bool
	// DescriptionPath is the sibling .txt sidecar path, populated when
	// StageRequest carried a Description or DescriptionFn and the sidecar
	// is on disk. Empty means: caller did not request one, or the sidecar
	// write failed (image stage itself still succeeds in that case).
	DescriptionPath string
}

// StageImageBytes fetches ImageURL through the resolved FileService, enforces
// the size cap, sniffs the magic bytes for an extension, and stages the bytes
// at OutputRoot/<KBBucket>/<CacheKey>.<ext> in the session workspace.
//
// Idempotency: if a previous call in the same SessionID already wrote a file
// at the destination path (URL-hint probe or sniffed-ext stat probe), the
// existing bytes are returned without re-downloading.
func StageImageBytes(ctx context.Context, req StageRequest) (*StageResult, error) {
	if req.SessionID == "" {
		return nil, fmt.Errorf("imagefetch: SessionID is required")
	}
	if req.ImageURL == "" {
		return nil, fmt.Errorf("imagefetch: ImageURL is required")
	}
	if req.FileSink == nil {
		return nil, fmt.Errorf("imagefetch: FileSink is required")
	}
	if req.CacheKey == "" {
		return nil, fmt.Errorf("imagefetch: CacheKey is required")
	}

	outDir := filepath.Join(OutputRoot, req.KBBucket)

	// 1. Pre-fetch multi-ext probe. A previous call may have staged the
	//    bytes under any of: the URL's extension hint OR a sniffed
	//    common image extension. We don't know which ext the prior call
	//    landed on (sniffing overrides URL hints when they disagree —
	//    e.g. a PNG uploaded with a `.tiff` URL still lands as .png).
	//    Probe all candidates BEFORE re-downloading so a second call
	//    with the same cache key is truly idempotent.
	probeExts := []string{}
	if hintExt := urlExt(req.URLHint); hintExt != "" {
		probeExts = append(probeExts, hintExt)
	}
	probeExts = append(probeExts, "png", "jpg", "gif", "bmp", "webp")
	for _, probeExt := range probeExts {
		probePath := filepath.Join(outDir, req.CacheKey+"."+probeExt)
		if stat, err := req.FileSink.StatSessionFile(ctx, req.SessionID, probePath); err == nil &&
			stat != nil && stat.Type == sandbox.RemoteEntryFile {
			logger.Infof(ctx, "[imagefetch] cache hit (probe): key=%s path=%s size=%d",
				req.CacheKey, probePath, stat.Size)
			return &StageResult{
				Path:            probePath,
				Size:            stat.Size,
				Ext:             probeExt,
				Cached:          true,
				DescriptionPath: probeExistingSidecar(ctx, req, outDir),
			}, nil
		}
	}

	// 2. Resolve the FileService for this provider URL. Falls back to the
	//    default when the resolver or tenant lookup fails (mirrors
	//    kb_get_image.go:resolveFileService).
	fileSvc := resolveFileService(ctx, req)

	// 3. Fetch bytes.
	reader, err := fileSvc.GetFile(ctx, req.ImageURL)
	if err != nil {
		return nil, fmt.Errorf("failed to read image bytes: %w", err)
	}
	data, readErr := io.ReadAll(reader)
	reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("failed to read image bytes: %w", readErr)
	}

	// 4. Size cap. Refused before staging to keep the sandbox clean.
	if len(data) > MaxBytes {
		return nil, fmt.Errorf(
			"image is %d bytes, over the %d-byte cap; resize and retry",
			len(data), MaxBytes,
		)
	}

	// 5. Magic-byte sniff with URL fallback.
	ext := DetectImageExt(data, req.ImageURL)
	if ext == "" {
		return nil, fmt.Errorf("unrecognized image format at %s", req.ImageURL)
	}
	outPath := filepath.Join(outDir, req.CacheKey+"."+ext)

	// 6. Authoritative stat by sniffed ext. Handles the case where a
	//    concurrent call raced us and wrote the file between our download
	//    and this stat.
	if stat, err := req.FileSink.StatSessionFile(ctx, req.SessionID, outPath); err == nil &&
		stat != nil && stat.Type == sandbox.RemoteEntryFile {
		logger.Infof(ctx, "[imagefetch] cache hit: key=%s path=%s size=%d",
			req.CacheKey, outPath, stat.Size)
		return &StageResult{
			Path:            outPath,
			Size:            stat.Size,
			Ext:             ext,
			Cached:          true,
			DescriptionPath: probeExistingSidecar(ctx, req, outDir),
		}, nil
	}

	// 7. Write to sandbox.
	if err := req.FileSink.EnsureSessionDir(ctx, req.SessionID, outDir); err != nil {
		return nil, fmt.Errorf("failed to stage image at %s: %w", outPath, err)
	}
	if err := req.FileSink.WriteSessionWorkspaceFile(ctx, req.SessionID, outPath, data); err != nil {
		return nil, fmt.Errorf("failed to stage image at %s: %w", outPath, err)
	}

	logger.Infof(ctx, "[imagefetch] staged: key=%s bucket=%s path=%s bytes=%d",
		req.CacheKey, req.KBBucket, outPath, len(data))

	// 8. Sidecar. Failure here MUST NOT fail the image stage — sidecar is
	//    an enhancement layer that gives downstream tools caption / source
	//    context. The image itself is already on disk and the agent can
	//    still proceed.
	descPath, derr := writeSidecar(ctx, req, outDir)
	if derr != nil {
		logger.Warnf(ctx, "[imagefetch] sidecar write failed (image stage succeeded): key=%s err=%v",
			req.CacheKey, derr)
	} else if descPath != "" {
		logger.Infof(ctx, "[imagefetch] sidecar staged: key=%s path=%s", req.CacheKey, descPath)
	}

	return &StageResult{Path: outPath, Size: int64(len(data)), Ext: ext, Cached: false, DescriptionPath: descPath}, nil
}

// resolveDescription picks the description text from StageRequest.Description
// or StageRequest.DescriptionFn. Empty result means "no sidecar requested" —
// callers should not write one. The fn form is preferred for callers whose
// description requires lookups (KB / Knowledge / WikiPage): the cost is only
// paid on the fresh-write path that actually fetches bytes, not on every
// cache-hit probe.
func resolveDescription(req StageRequest) (string, error) {
	if req.Description != "" {
		return req.Description, nil
	}
	if req.DescriptionFn != nil {
		return req.DescriptionFn()
	}
	return "", nil
}

// writeSidecar writes the sidecar file at <outDir>/<cacheKey>.txt and
// returns its path. Idempotent: if a previous call in the same session
// already wrote the sidecar, the existing file's path is returned without
// re-writing. Returns ("", nil) when no description was requested. The
// caller is expected to log + degrade on error (do not fail the stage).
func writeSidecar(ctx context.Context, req StageRequest, outDir string) (string, error) {
	text, err := resolveDescription(req)
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", nil
	}
	descPath := sidecarPath(outDir, req.CacheKey)
	if stat, serr := req.FileSink.StatSessionFile(ctx, req.SessionID, descPath); serr == nil &&
		stat != nil && stat.Type == sandbox.RemoteEntryFile {
		return descPath, nil
	}
	if err := req.FileSink.EnsureSessionDir(ctx, req.SessionID, filepath.Dir(descPath)); err != nil {
		return "", fmt.Errorf("failed to stage description at %s: %w", descPath, err)
	}
	if err := req.FileSink.WriteSessionWorkspaceFile(ctx, req.SessionID, descPath, []byte(text)); err != nil {
		return "", fmt.Errorf("failed to stage description at %s: %w", descPath, err)
	}
	return descPath, nil
}

// sidecarPath returns the conventional sidecar path for a given (outDir,
// cacheKey). Existence is not checked.
func sidecarPath(outDir, cacheKey string) string {
	return filepath.Join(outDir, cacheKey+DescriptionExt)
}

// probeExistingSidecar returns the sidecar path if a sidecar file exists on
// disk at the conventional location, "" otherwise. Used by cache-hit paths
// to populate StageResult.DescriptionPath without re-running writeSidecar
// (which would otherwise re-evaluate DescriptionFn and re-write the file).
func probeExistingSidecar(ctx context.Context, req StageRequest, outDir string) string {
	if req.FileSink == nil {
		return ""
	}
	descPath := sidecarPath(outDir, req.CacheKey)
	if stat, err := req.FileSink.StatSessionFile(ctx, req.SessionID, descPath); err == nil &&
		stat != nil && stat.Type == sandbox.RemoteEntryFile {
		return descPath
	}
	return ""
}

// resolveFileService picks the FileService for a provider URL. When the
// resolver returns an error (e.g. tenant lookup fails or no backend is
// configured) we fall back to req.FileService, which the chat-side already
// uses as the tenant default — matches the kb_get_image.go:resolveFileService
// fallback policy.
func resolveFileService(ctx context.Context, req StageRequest) interfaces.FileService {
	if req.FileService == nil {
		return nil
	}
	if req.StorageResolver == nil || req.TenantID == 0 {
		return req.FileService
	}
	if req.TenantService == nil {
		return req.FileService
	}

	provider := types.ParseProviderScheme(req.ImageURL)
	backendID, _, _ := types.ParseStorageBackendPath(req.ImageURL)

	tenant, terr := req.TenantService.GetTenantByID(ctx, req.TenantID)
	if terr != nil || tenant == nil {
		logger.Warnf(ctx, "[imagefetch] tenant lookup failed (falling back to default): tenant=%d err=%v",
			req.TenantID, terr)
		return req.FileService
	}

	baseDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	fileSvc, resolvedProvider, svcErr := req.StorageResolver.ResolveFileService(
		ctx, tenant, backendID, provider, baseDir,
	)
	if svcErr != nil {
		logger.Warnf(ctx, "[imagefetch] resolve file service failed (falling back to default): provider=%s err=%v",
			resolvedProvider, svcErr)
		return req.FileService
	}
	if fileSvc == nil {
		return req.FileService
	}
	return fileSvc
}

// DetectImageExt picks a file extension for an image. The stdlib
// http.DetectContentType covers PNG/JPEG/GIF/BMP/WebP from magic bytes —
// sufficient for KB-stored images, which are produced by doc parsers the
// project controls. Falls back to the URL extension when magic detection
// fails (TIFF and other formats the stdlib does not recognize).
func DetectImageExt(data []byte, fallbackURL string) string {
	sniff := data
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	switch http.DetectContentType(sniff) {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/gif":
		return "gif"
	case "image/bmp":
		return "bmp"
	case "image/webp":
		return "webp"
	}
	return urlExt(fallbackURL)
}

// urlExt returns the lowercased file extension of a URL, or "" when the URL
// has none. Used as a pre-flight cache hint so the second call with the same
// cache key can skip the download.
func urlExt(rawURL string) string {
	return strings.ToLower(strings.TrimPrefix(path.Ext(rawURL), "."))
}
