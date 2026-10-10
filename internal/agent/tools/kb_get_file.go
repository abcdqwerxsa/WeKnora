package tools

// Package tools — kb_get_file.
//
// Pulls one KB document's source file (the user's uploaded PDF / DOCX
// / XLSX, NOT the extracted text) into the session sandbox at
// `/workspace/input/<sha6>/<filename>`, so downstream skills can
// read the original file from disk instead of relying on chat
// attachment re-upload every turn.
//
// Design notes:
//   - Input is a knowledge_id (the canonical UUID returned by
//     knowledge_search / get_document_info). Same authorization
//     boundary as kb_get_image / get_document_info:
//     authorizeKnowledgeInSearchTargets refuses out-of-scope and
//     cross-tenant lookups with the same generic "document not found"
//     message a real 404 returns, never a distinguishable error.
//   - File type guard: only `Type == "file"` knowledge rows with a
//     non-empty FilePath are accepted. URL-type and passage-type rows
//     have no local bytes; the tool refuses with a clear error pointing
//     the agent at get_document_info.
//   - The byte-fetch + FileService resolution lives in the shared
//     ResolveFileServiceForKnowledge helper, so cross-tenant /
//     shared-KB behaviour matches data_analysis byte-for-byte and
//     stays in sync as storage backends are added.
//   - Path convention mirrors sandboxAttachmentPath (session_attachment_staging.go:246):
//     `/workspace/input/<hex(sha256(FilePath)[:6])>/<filename>`.
//     This means a KB-pulled file lives in the SAME tree as chat
//     attachments — downstream skills see one consistent filesystem
//     regardless of where the file came from.
//   - Files in /workspace/input are read-only for agents
//     (sandbox_write.go refuses writes there). To edit, the agent
//     must first copy the file to /workspace/output. The tool
//     description surfaces this contract so the model never tries to
//     mutate the staged file in place.
//   - Idempotency: when the target file already exists with a size
//     matching Knowledge.FileSize, the second call returns `cached:
//     true` without re-downloading. A concurrent race window is
//     tolerable (WriteSessionInputFile last-write-wins, no data
//     corruption) and mirrors kb_get_image's tolerance.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

// kbGetFileDescription is the model-facing prompt for the tool. Kept as a
// package-level constant for readability; no per-call metadata is folded in.
const kbGetFileDescription = `Read one KB document's source file into the session sandbox.

## Usage
- Call when a downstream skill needs the original uploaded file on disk.
  Chat attachments live in the same /workspace/input directory, so
  kb_get_file avoids re-uploading tender documents each turn.
- ` + "`knowledge_id`" + ` is the canonical UUID returned by
  ` + "`knowledge_search`" + ` / ` + "`get_document_info`" + `. The model
  context layer is expected to translate request-local handles
  (res://, dN) before the tool is invoked.
- The returned ` + "`path`" + ` lives under
  ` + "`/workspace/input/<sha6>/<filename>`" + `. Read it with
  ` + "`read_sandbox_file`" + `, pass it as a shell argument, or hand it
  to a skill script. The file is READ-ONLY: agents that need to edit
  must copy it to ` + "`/workspace/output/`" + ` first.
- Multiple calls with the same ` + "`knowledge_id`" + ` in one session are
  idempotent: the second call returns ` + "`cached: true`" + ` without
  re-downloading.
- The optional ` + "`filename`" + ` override replaces the source file's
  display name; the file extension is always taken from the source's
  parsed type (e.g. PDF stays .pdf even when the override omits it).

## When to Use
- The user uploaded a tender / spec / contract PDF/DOCX/XLSX to a bound
  KB once, and now wants a skill to read it from disk.
- The chat already references a KB document and a downstream skill
  needs the source file path, not chunked text.

## When NOT to Use
- For chunked text — use ` + "`knowledge_search`" + ` /
  ` + "`list_knowledge_chunks`" + ` / ` + "`get_document_info`" + `.
- For URL-type or passage-type Knowledge — kb_get_file only stages
  source-file attachments. Use ` + "`get_document_info`" + ` for
  metadata.
- For files outside the agent's bound KBs — out-of-scope documents
  return the same generic "document not found" as a real 404.
- For files the user has not yet uploaded — kb_get_file stages
  documents that are already in the KB. New uploads must go through
  the KB ingest path first.

## Path Rules
- The returned path is absolute under ` + "`/workspace/input/`" + `.
- Do not edit the staged file directly. Copy it to ` + "`/workspace/output/`" + `
  before mutating; ` + "`/workspace/input`" + ` is reserved and write-blocked by
  the sandbox.
- Same path convention as chat-uploaded attachments
  (` + "`sandboxAttachmentPath`" + `): the hash prefix is
  ` + "`sha256(FilePath)[:6]`" + `. Two different Knowledge rows that
  happen to share a ` + "`FilePath`" + ` (rare; re-ingested blob) will
  share the staged path — intended idempotent behaviour.

## Size Handling
- Byte cap is ` + "`utils.GetMaxFileSizeMB() * 1 MiB`" + ` — same cap
  as chat attachment staging. Default 50 MiB; configurable via
  ` + "`MAX_FILE_SIZE_MB`" + `. Larger files are refused with a clear
  error rather than truncated.

## Returns
- ` + "`{path, size, kb_id, knowledge_id, filename, file_type, cached, source_url, document_title}`" + ` JSON object.
  ` + "`source_url`" + ` is the KB's provider:// URL of the staged blob
  (provenance for downstream scripts). ` + "`document_title`" + ` is the
  Knowledge row's ` + "`Title`" + ` — same field ` + "`get_document_info`" + `
  returns; copied here so a downstream call does not need a second
  lookup.`

// KBGetFileInput is the input schema for kb_get_file.
type KBGetFileInput struct {
	KnowledgeID string `json:"knowledge_id" jsonschema:"Canonical UUID of the KB document to stage. Returned by knowledge_search / get_document_info. The model context layer is expected to translate request-local handles (res://, dN) before the tool is invoked."`
	Filename    string `json:"filename,omitempty" jsonschema:"Optional filename override; replaces Knowledge.FileName. The file extension is always taken from Knowledge.FileType and is preserved on the staged path even when this override omits it."`
}

// KBGetFileTool reads one KB document's source file out of the bound KB
// into the session sandbox so downstream skills can read it from disk
// instead of relying on chat attachment re-upload.
type KBGetFileTool struct {
	BaseTool
	fileService          interfaces.FileService
	knowledgeService     interfaces.KnowledgeService
	knowledgeBaseService interfaces.KnowledgeBaseService
	storageResolver      interfaces.StorageBackendResolver
	tenantService        interfaces.TenantService
	searchTargets        types.SearchTargets
	fileSink             sandbox.SessionFileStore
	// localBaseDir is the LOCAL_STORAGE_BASE_DIR value captured at
	// construction time. Re-reading os.Getenv on each call would
	// produce an empty string when the variable was set programmatically
	// before startup, causing the local FileService to fall back to the
	// /data/files default and miss the actual files (#1040). Mirrors
	// data_analysis.go:124.
	localBaseDir string
}

// NewKBGetFileTool constructs the tool.
//
//   - fileService is the tenant default used when the resolver cannot
//     pick a backend-specific one (matches the fallback in
//     image_multimodal.go:resolveFileServiceForPayload).
//   - knowledgeService powers scope authorization via
//     authorizeKnowledgeInSearchTargets (GetKnowledgeByIDOnly).
//   - knowledgeBaseService is currently unused by Execute; reserved
//     for a future per-KB metadata lookup. Accepting the dependency
//     keeps the constructor symmetric with NewKBGetImageTool and
//     NewDataAnalysisTool so the wiring in agent_service.go stays
//     uniform.
//   - storageResolver picks the correct backend for a provider:// URL.
//   - tenantService supplies the *types.Tenant the resolver requires;
//     a nil tenant makes ResolveFileService refuse.
//   - searchTargets is the agent's resolved KB scope; a Knowledge row
//     whose KB is not in this set is treated as not-found, never as
//     cross-tenant leak.
//   - fileSink MUST NOT be nil — it is where the bytes land.
func NewKBGetFileTool(
	fileService interfaces.FileService,
	knowledgeService interfaces.KnowledgeService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	storageResolver interfaces.StorageBackendResolver,
	tenantService interfaces.TenantService,
	searchTargets types.SearchTargets,
	fileSink sandbox.SessionFileStore,
) *KBGetFileTool {
	return &KBGetFileTool{
		BaseTool: NewBaseTool(
			ToolKBGetFile,
			kbGetFileDescription,
			utils.GenerateSchema[KBGetFileInput](),
		),
		fileService:          fileService,
		knowledgeService:     knowledgeService,
		knowledgeBaseService: knowledgeBaseService,
		storageResolver:      storageResolver,
		tenantService:        tenantService,
		searchTargets:        searchTargets,
		fileSink:             fileSink,
		localBaseDir:         strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR")),
	}
}

// Execute pulls the source file for the requested knowledge_id and
// stages it under /workspace/input/<sha6>/<filename>. Returns the
// absolute path plus size and provenance metadata so downstream skills
// can embed or read it without a second lookup round-trip.
func (t *KBGetFileTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][KBGetFile] Execute started")

	var input KBGetFileInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, nil
	}
	if strings.TrimSpace(input.KnowledgeID) == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "knowledge_id is required",
		}, nil
	}

	sessionID := resolveSessionID(ctx)
	if sessionID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "no session ID in context; kb_get_file must run inside an agent turn",
		}, nil
	}

	// 1. Authorize. Returns the same generic "document not found" as a
	//    real 404 for out-of-scope and cross-tenant lookups, so a
	//    model cannot enumerate documents by alternating knowledge_id
	//    inputs.
	knowledge, err := authorizeKnowledgeInSearchTargets(
		ctx, t.searchTargets, input.KnowledgeID, t.knowledgeService,
	)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("document not found: %s", input.KnowledgeID),
		}, nil
	}

	// 2. File-type guard. URL-type and passage-type Knowledge rows
	//    have no local bytes (FilePath is empty or the row only carries
	//    a Source URL / inline text). Refuse with a clear error
	//    pointing the agent at get_document_info.
	if !strings.EqualFold(strings.TrimSpace(knowledge.Type), "file") {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"document %s is type %q, not \"file\"; kb_get_file only stages source-file "+
					"attachments. For URL-type or passage-type sources use get_document_info, "+
					"and re-upload the file into the KB if you want it staged into the sandbox.",
				input.KnowledgeID, knowledge.Type),
		}, nil
	}
	if strings.TrimSpace(knowledge.FilePath) == "" {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"document %s has no FilePath; the source file was not uploaded to the KB. "+
					"Use get_document_info to confirm the document is file-backed, or re-upload "+
					"the file into the KB before retrying.",
				input.KnowledgeID),
		}, nil
	}

	// 3. Resolve staged filename. The optional input.filename
	//    overrides Knowledge.FileName; the extension always comes from
	//    Knowledge.FileType so downstream scripts see a recognizable
	//    file (pandoc/Lo PDF refuse unknown extensions).
	filename := strings.TrimSpace(input.Filename)
	if filename == "" {
		filename = knowledge.FileName
	}
	safe, sfnErr := utils.SafeFileName(filename)
	if sfnErr != nil || safe == "" {
		// SafeFileName rejects "" but our trim above already covers
		// the empty case; any remaining error is the caller passing a
		// path-traversal or NUL-laden name. Fall back to the
		// canonical knowledge id so the stage still succeeds.
		logger.Warnf(ctx, "[Tool][KBGetFile] filename %q rejected by SafeFileName, falling back to id: %v",
			filename, sfnErr)
		safe = knowledge.ID
	}
	ext := strings.ToLower(strings.TrimSpace(knowledge.FileType))
	if ext != "" && !strings.HasSuffix(strings.ToLower(safe), "."+ext) {
		safe = safe + "." + ext
	}

	// 4. Compute target path. Mirror sandboxAttachmentPath: hash the
	//    durable FilePath (not the knowledge_id) so a re-ingested blob
	//    that keeps the same URL lands at the same slot.
	sum := sha256.Sum256([]byte(knowledge.FilePath))
	remotePath := path.Join(sandbox.SessionInputRoot, fmt.Sprintf("%x", sum[:6]), safe)

	// 5. Idempotency probe. If the file is already staged at the
	//    expected size, skip the download and report cached=true. A
	//    concurrent race is tolerable: the second writer wins, no data
	//    corruption. Knowledge.FileSize may be 0 for legacy rows;
	//    skip the cache check in that case to avoid an always-cached
	//    stub.
	if knowledge.FileSize > 0 {
		if stat, statErr := t.fileSink.StatSessionFile(ctx, sessionID, remotePath); statErr == nil &&
			stat != nil && stat.Type == sandbox.RemoteEntryFile &&
			stat.Size == knowledge.FileSize {
			logger.Infof(ctx, "[Tool][KBGetFile] cache hit: knowledge=%s kb=%s path=%s size=%d",
				input.KnowledgeID, knowledge.KnowledgeBaseID, remotePath, stat.Size)
			return kbGetFileResult(remotePath, stat.Size, knowledge, safe, ext, true), nil
		}
	}

	// 6. Resolve FileService. The shared helper picks the correct
	//    provider/backend for cross-tenant / shared-KB scenarios and
	//    falls back to the tenant default on any failure.
	fileSvc := ResolveFileServiceForKnowledge(
		ctx, knowledge, sessionID, "[Tool][KBGetFile][storage]",
		t.knowledgeBaseService, t.tenantService, t.storageResolver,
		t.fileService, t.localBaseDir,
	)

	// 7. Fetch bytes with size cap. Use io.LimitReader so a 51 MiB
	//    blob on a 50 MiB cap fails fast (one byte over the limit is
	//    the only reliable signal that the cap was exceeded).
	maxBytes := utils.GetMaxFileSizeMB() * 1024 * 1024
	reader, getErr := fileSvc.GetFile(ctx, knowledge.FilePath)
	if getErr != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to open source file for %s: %v", input.KnowledgeID, getErr),
		}, nil
	}
	content, readErr := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to read source file for %s: %v", input.KnowledgeID, readErr),
		}, nil
	}
	if closeErr != nil {
		logger.Warnf(ctx, "[Tool][KBGetFile] reader close error (ignored): %v", closeErr)
	}
	if int64(len(content)) > maxBytes {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"document %s exceeds sandbox staging limit of %d bytes; resize before retrying",
				input.KnowledgeID, maxBytes),
		}, nil
	}

	// 8. Ensure dir + write. EnsureSessionDir is a no-op when the dir
	//    already exists, so concurrent calls do not race.
	if dirErr := t.fileSink.EnsureSessionDir(ctx, sessionID, path.Dir(remotePath)); dirErr != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to stage file under %s: %v", path.Dir(remotePath), dirErr),
		}, nil
	}
	if writeErr := t.fileSink.WriteSessionInputFile(ctx, sessionID, remotePath, content); writeErr != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to stage file at %s: %v", remotePath, writeErr),
		}, nil
	}

	logger.Infof(ctx, "[Tool][KBGetFile] staged: knowledge=%s kb=%s path=%s bytes=%d",
		input.KnowledgeID, knowledge.KnowledgeBaseID, remotePath, len(content))

	return kbGetFileResult(remotePath, int64(len(content)), knowledge, safe, ext, false), nil
}

// Cleanup releases any resources held by the tool. Currently a no-op —
// the FileService and SessionFileStore are owned by the agentService.
func (t *KBGetFileTool) Cleanup(ctx context.Context) error {
	return nil
}

// kbGetFileResult builds the standard success result for the tool.
// Used for both fresh writes and cache hits so the response shape is
// identical. The Data payload mirrors kb_get_image's, plus
// filename / file_type / source_url / document_title so downstream
// consumers do not need a second lookup to embed or reference the file.
func kbGetFileResult(
	remotePath string,
	size int64,
	knowledge *types.Knowledge,
	filename string,
	ext string,
	cached bool,
) *types.ToolResult {
	var cacheNote string
	if cached {
		cacheNote = " (already staged earlier in this session)"
	}
	var output strings.Builder
	fmt.Fprintf(&output,
		"File staged at %s (%d bytes, %s)%s\n\n"+
			"Source: KB %s / document %q (knowledge_id=%s)\n"+
			"Original URL: %s\n\n"+
			"Reference this path by passing it to a downstream skill script, "+
			"reading it with read_sandbox_file, or using it as a shell argument. "+
			"The file is read-only; copy it to /workspace/output/ before mutating.",
		remotePath, size, ext, cacheNote,
		knowledge.KnowledgeBaseID, knowledge.Title, knowledge.ID,
		knowledge.FilePath,
	)
	return &types.ToolResult{
		Success: true,
		Output:  output.String(),
		Data: map[string]interface{}{
			"display_type":   ToolKBGetFile,
			"path":           remotePath,
			"size":           size,
			"kb_id":          knowledge.KnowledgeBaseID,
			"knowledge_id":   knowledge.ID,
			"filename":       filename,
			"file_type":      ext,
			"cached":         cached,
			"source_url":     knowledge.FilePath,
			"document_title": knowledge.Title,
		},
	}
}
