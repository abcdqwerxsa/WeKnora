package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubKBFileKnowledgeService is a knowledgeService stub that satisfies
// GetKnowledgeByIDOnly (which authorizeKnowledgeInSearchTargets calls)
// without touching the production DB.
type stubKBFileKnowledgeService struct {
	interfaces.KnowledgeService
	getByIDOnly func(ctx context.Context, id string) (*types.Knowledge, error)
	getByID     func(ctx context.Context, id string) (*types.Knowledge, error)
}

func (s *stubKBFileKnowledgeService) GetKnowledgeByIDOnly(ctx context.Context, id string) (*types.Knowledge, error) {
	if s.getByIDOnly != nil {
		return s.getByIDOnly(ctx, id)
	}
	return nil, errors.New("knowledge stub: not implemented")
}

func (s *stubKBFileKnowledgeService) GetKnowledgeByID(ctx context.Context, id string) (*types.Knowledge, error) {
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return nil, errors.New("knowledge stub: not implemented")
}

// memInputFileStore extends memFileStore with WriteSessionInputFile so the
// tool can write under /workspace/input. The base memFileStore only
// implements WriteSessionWorkspaceFile.
type memInputFileStore struct {
	*memFileStore
}

func (m *memInputFileStore) WriteSessionInputFile(ctx context.Context, sessionID, path string, content []byte) error {
	return m.memFileStore.WriteSessionWorkspaceFile(ctx, sessionID, path, content)
}

// kbFileMakeKnowledge builds a Knowledge row with sane defaults for tests.
func kbFileMakeKnowledge(id, kbID string, opts ...func(*types.Knowledge)) *types.Knowledge {
	k := &types.Knowledge{
		ID:              id,
		KnowledgeBaseID: kbID,
		TenantID:        7,
		Type:            "file",
		Title:           "2026Q1 标书",
		FileName:        "招标文件.pdf",
		FileType:        "pdf",
		FilePath:        "provider://bucket/" + id,
		FileSize:        9,
	}
	for _, o := range opts {
		o(k)
	}
	return k
}

func withKnowledgeType(t string) func(*types.Knowledge) {
	return func(k *types.Knowledge) { k.Type = t }
}

func withKnowledgeFilePath(p string) func(*types.Knowledge) {
	return func(k *types.Knowledge) { k.FilePath = p }
}

func withKnowledgeFileName(n string) func(*types.Knowledge) {
	return func(k *types.Knowledge) { k.FileName = n }
}

func withKnowledgeFileType(t string) func(*types.Knowledge) {
	return func(k *types.Knowledge) { k.FileType = t }
}

func withKnowledgeFileSize(s int64) func(*types.Knowledge) {
	return func(k *types.Knowledge) { k.FileSize = s }
}

func withKnowledgeTenantID(id uint64) func(*types.Knowledge) {
	return func(k *types.Knowledge) { k.TenantID = id }
}

// newKBFileTestTool wires a KBGetFileTool with stub deps that satisfy the
// common success path. Tests override individual deps on the returned
// struct to exercise error paths. The default knowledge stub returns
// "not found" so tests that expect success must overwrite
// tool.knowledgeService with a stub that returns the desired row.
func newKBFileTestTool(t *testing.T) (*KBGetFileTool, *memInputFileStore, *stubFileService) {
	t.Helper()
	base := newMemFileStore()
	store := &memInputFileStore{memFileStore: base}
	fileSvc := &stubFileService{
		getFile: func(ctx context.Context, p string) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader([]byte("file-body"))), nil
		},
	}
	resolver := &stubResolver{
		resolve: func(ctx context.Context, tnt *types.Tenant, backendID, provider, baseDir string) (
			interfaces.FileService, string, error) {
			return fileSvc, provider, nil
		},
	}
	tenants := &stubTenantService{
		getByID: func(ctx context.Context, id uint64) (*types.Tenant, error) {
			return &types.Tenant{ID: id}, nil
		},
	}
	targets := types.SearchTargets{
		{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-A", TenantID: 7},
		{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-B", TenantID: 7},
	}
	knowledge := &stubKBFileKnowledgeService{
		getByIDOnly: func(ctx context.Context, id string) (*types.Knowledge, error) {
			return nil, errors.New("knowledge stub: not implemented")
		},
	}
	kbases := &stubKBKnowledgeBaseService{
		getByID: func(ctx context.Context, id string) (*types.KnowledgeBase, error) {
			return nil, errors.New("kb stub: not implemented")
		},
	}
	tool := NewKBGetFileTool(fileSvc, knowledge, kbases, resolver, tenants, targets, store)
	return tool, store, fileSvc
}

func mustMarshal(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// withSessionKBFile returns a context that resolveSessionID recognizes
// as belonging to "test-session". Mirrors kb_get_image_test's helper.
func withSessionKBFile(ctx context.Context) context.Context {
	return types.WithSessionID(ctx, "test-session")
}

func TestKBGetFile_CompilesAsTool(t *testing.T) {
	// Compile-time guard: the tool must satisfy the types.Tool interface.
	var _ types.Tool = (*KBGetFileTool)(nil)
}

func TestKBGetFile_SuccessPath_PDF(t *testing.T) {
	tool, store, fileSvc := newKBFileTestTool(t)
	// Register a single knowledge row, indexed by id, and inject it into
	// the stub. We use a closure inside the knowledge stub via a custom
	// approach below.
	tool.knowledgeService = &stubKBFileKnowledgeService{
		getByIDOnly: func(ctx context.Context, id string) (*types.Knowledge, error) {
			if id == "k-1" {
				return kbFileMakeKnowledge("k-1", "kb-A"), nil
			}
			return nil, errors.New("not found")
		},
	}
	atomic.StoreInt32(&fileSvc.getFileHits, 0)

	res, err := tool.Execute(withSessionKBFile(context.Background()), mustMarshal(t, KBGetFileInput{
		KnowledgeID: "k-1",
	}))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}
	if path, _ := res.Data["path"].(string); path == "" {
		t.Fatalf("expected non-empty path, got %v", res.Data)
	}
	if got, want := res.Data["kb_id"], "kb-A"; got != want {
		t.Fatalf("kb_id = %v, want %v", got, want)
	}
	if got, want := res.Data["knowledge_id"], "k-1"; got != want {
		t.Fatalf("knowledge_id = %v, want %v", got, want)
	}
	if got, want := res.Data["file_type"], "pdf"; got != want {
		t.Fatalf("file_type = %v, want %v", got, want)
	}
	if got, want := res.Data["cached"], false; got != want {
		t.Fatalf("cached = %v, want %v", got, want)
	}
	if got, want := res.Data["source_url"], "provider://bucket/k-1"; got != want {
		t.Fatalf("source_url = %v, want %v", got, want)
	}
	if got, want := res.Data["document_title"], "2026Q1 标书"; got != want {
		t.Fatalf("document_title = %v, want %v", got, want)
	}
	if got := atomic.LoadInt32(&fileSvc.getFileHits); got != 1 {
		t.Fatalf("expected 1 GetFile call, got %d", got)
	}
	// File should be on the store under the expected path.
	if !store.has("test-session", res.Data["path"].(string)) {
		t.Fatalf("file not found at %s", res.Data["path"])
	}
}

func TestKBGetFile_PreservesDocxExt(t *testing.T) {
	tool, store, _ := newKBFileTestTool(t)
	tool.knowledgeService = &stubKBFileKnowledgeService{
		getByIDOnly: func(ctx context.Context, id string) (*types.Knowledge, error) {
			return kbFileMakeKnowledge("k-1", "kb-A",
				withKnowledgeFileName("without-ext"),
				withKnowledgeFileType("docx"),
				withKnowledgeFilePath("provider://bucket/k-1")), nil
		},
	}
	res, err := tool.Execute(withSessionKBFile(context.Background()), mustMarshal(t, KBGetFileInput{
		KnowledgeID: "k-1",
	}))
	if err != nil || !res.Success {
		t.Fatalf("Execute failed: %v %+v", err, res)
	}
	path := res.Data["path"].(string)
	if !endsWith(path, ".docx") {
		t.Fatalf("expected path to end with .docx, got %s", path)
	}
	if !store.has("test-session", path) {
		t.Fatalf("file not found at %s", path)
	}
}

func TestKBGetFile_FilenameOverride(t *testing.T) {
	tool, _, _ := newKBFileTestTool(t)
	tool.knowledgeService = &stubKBFileKnowledgeService{
		getByIDOnly: func(ctx context.Context, id string) (*types.Knowledge, error) {
			return kbFileMakeKnowledge("k-1", "kb-A",
				withKnowledgeFileName("orig.pdf"),
				withKnowledgeFileType("pdf"),
				withKnowledgeFilePath("provider://bucket/k-1")), nil
		},
	}
	res, err := tool.Execute(withSessionKBFile(context.Background()), mustMarshal(t, KBGetFileInput{
		KnowledgeID: "k-1",
		Filename:    "custom-name",
	}))
	if err != nil || !res.Success {
		t.Fatalf("Execute failed: %v %+v", err, res)
	}
	if got, want := res.Data["filename"], "custom-name.pdf"; got != want {
		t.Fatalf("filename = %v, want %v", got, want)
	}
}

func TestKBGetFile_RejectsURLType(t *testing.T) {
	tool, _, _ := newKBFileTestTool(t)
	tool.knowledgeService = &stubKBFileKnowledgeService{
		getByIDOnly: func(ctx context.Context, id string) (*types.Knowledge, error) {
			return kbFileMakeKnowledge("k-1", "kb-A",
				withKnowledgeType("url"),
				withKnowledgeFilePath("")), nil
		},
	}
	res, err := tool.Execute(withSessionKBFile(context.Background()), mustMarshal(t, KBGetFileInput{
		KnowledgeID: "k-1",
	}))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.Success {
		t.Fatalf("expected failure for url-type knowledge")
	}
	if !kbFileContains(res.Error, "type") || !kbFileContains(res.Error, "url") {
		t.Fatalf("expected error to mention type=url, got %q", res.Error)
	}
}

func TestKBGetFile_RejectsPassageType(t *testing.T) {
	tool, _, _ := newKBFileTestTool(t)
	tool.knowledgeService = &stubKBFileKnowledgeService{
		getByIDOnly: func(ctx context.Context, id string) (*types.Knowledge, error) {
			return kbFileMakeKnowledge("k-1", "kb-A",
				withKnowledgeType("passage"),
				withKnowledgeFilePath("")), nil
		},
	}
	res, _ := tool.Execute(withSessionKBFile(context.Background()), mustMarshal(t, KBGetFileInput{
		KnowledgeID: "k-1",
	}))
	if res.Success {
		t.Fatalf("expected failure for passage-type knowledge")
	}
	if !kbFileContains(res.Error, "passage") {
		t.Fatalf("expected error to mention type=passage, got %q", res.Error)
	}
}

func TestKBGetFile_RejectsEmptyFilePath(t *testing.T) {
	tool, _, _ := newKBFileTestTool(t)
	tool.knowledgeService = &stubKBFileKnowledgeService{
		getByIDOnly: func(ctx context.Context, id string) (*types.Knowledge, error) {
			return kbFileMakeKnowledge("k-1", "kb-A", withKnowledgeFilePath("")), nil
		},
	}
	res, _ := tool.Execute(withSessionKBFile(context.Background()), mustMarshal(t, KBGetFileInput{
		KnowledgeID: "k-1",
	}))
	if res.Success {
		t.Fatalf("expected failure for empty FilePath")
	}
	if !kbFileContains(res.Error, "no FilePath") {
		t.Fatalf("expected error to mention no FilePath, got %q", res.Error)
	}
}

func TestKBGetFile_KBOutOfScope_GenericNotFound(t *testing.T) {
	tool, _, _ := newKBFileTestTool(t)
	tool.knowledgeService = &stubKBFileKnowledgeService{
		getByIDOnly: func(ctx context.Context, id string) (*types.Knowledge, error) {
			// Knowledge row exists but its KB is not in the agent's scope.
			return kbFileMakeKnowledge("k-1", "kb-OUTSIDE"), nil
		},
	}
	res, _ := tool.Execute(withSessionKBFile(context.Background()), mustMarshal(t, KBGetFileInput{
		KnowledgeID: "k-1",
	}))
	if res.Success {
		t.Fatalf("expected failure for out-of-scope KB")
	}
	// Must be indistinguishable from a real 404 — no leak about KB-OUTSIDE.
	if !kbFileContains(res.Error, "document not found") {
		t.Fatalf("expected generic 'document not found', got %q", res.Error)
	}
	if kbFileContains(res.Error, "kb-OUTSIDE") {
		t.Fatalf("error leaked KB id: %q", res.Error)
	}
}

func TestKBGetFile_CrossTenant_ResolvesViaStorage(t *testing.T) {
	tool, store, _ := newKBFileTestTool(t)
	// Storage resolver picks a backend-specific FileService whose
	// GetFile returns a distinct payload. We then assert that payload
	// made it into the store.
	resolverPicked := &stubFileService{
		getFile: func(ctx context.Context, p string) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader([]byte("from-remote-backend"))), nil
		},
	}
	tool.storageResolver = &stubResolver{
		resolve: func(ctx context.Context, tnt *types.Tenant, backendID, provider, baseDir string) (
			interfaces.FileService, string, error) {
			return resolverPicked, "remote", nil
		},
	}
	tool.knowledgeService = &stubKBFileKnowledgeService{
		getByIDOnly: func(ctx context.Context, id string) (*types.Knowledge, error) {
			return kbFileMakeKnowledge("k-1", "kb-A",
				withKnowledgeTenantID(99)), nil
		},
	}
	// Override KB lookup so the shared resolver actually picks the
	// backend-specific service (the default fixture returns "not
	// implemented", which would short-circuit to the fallback).
	tool.knowledgeBaseService = &stubKBKnowledgeBaseService{
		getByID: func(ctx context.Context, id string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{
				ID:    id,
				Name:  "Cross-tenant test KB",
				Type:  "document",
				StorageProviderConfig: &types.StorageProviderConfig{
					Provider: "remote",
				},
			}, nil
		},
	}
	res, err := tool.Execute(withSessionKBFile(context.Background()), mustMarshal(t, KBGetFileInput{
		KnowledgeID: "k-1",
	}))
	if err != nil || !res.Success {
		t.Fatalf("Execute failed: %v %+v", err, res)
	}
	if !store.has("test-session", res.Data["path"].(string)) {
		t.Fatalf("file not staged")
	}
	body, _ := store.ReadSessionFile(context.Background(), "test-session", res.Data["path"].(string))
	if string(body) != "from-remote-backend" {
		t.Fatalf("expected bytes from remote backend, got %q", body)
	}
}

func TestKBGetFile_Oversize_Refused(t *testing.T) {
	tool, store, fileSvc := newKBFileTestTool(t)
	tool.knowledgeService = &stubKBFileKnowledgeService{
		getByIDOnly: func(ctx context.Context, id string) (*types.Knowledge, error) {
			return kbFileMakeKnowledge("k-1", "kb-A",
				withKnowledgeFileSize(0)), nil
		},
	}
	// Build a payload that exceeds the cap. We use MAX_FILE_SIZE_MB's
	// default 50 MiB + 1 byte to deterministically trip the cap.
	big := bytes.Repeat([]byte("a"), 50*1024*1024+1)
	atomic.StoreInt32(&fileSvc.getFileHits, 0)
	tool.fileService = &stubFileService{
		getFile: func(ctx context.Context, p string) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(big)), nil
		},
	}
	res, _ := tool.Execute(withSessionKBFile(context.Background()), mustMarshal(t, KBGetFileInput{
		KnowledgeID: "k-1",
	}))
	if res.Success {
		t.Fatalf("expected failure for oversize file")
	}
	if !kbFileContains(res.Error, "exceeds sandbox staging limit") {
		t.Fatalf("expected oversize error, got %q", res.Error)
	}
	// Sandbox must remain empty — refuse before write.
	if len(store.files) != 0 {
		t.Fatalf("expected sandbox empty after oversize refusal, got %d files", len(store.files))
	}
}

func TestKBGetFile_Idempotency_SecondCallCached(t *testing.T) {
	tool, store, fileSvc := newKBFileTestTool(t)
	tool.knowledgeService = &stubKBFileKnowledgeService{
		getByIDOnly: func(ctx context.Context, id string) (*types.Knowledge, error) {
			return kbFileMakeKnowledge("k-1", "kb-A",
				withKnowledgeFileSize(9)), nil // matches the 9-byte body
		},
	}
	ctx := withSessionKBFile(context.Background())
	atomic.StoreInt32(&fileSvc.getFileHits, 0)

	// First call: download + write.
	res1, err := tool.Execute(ctx, mustMarshal(t, KBGetFileInput{KnowledgeID: "k-1"}))
	if err != nil || !res1.Success {
		t.Fatalf("first Execute failed: %v %+v", err, res1)
	}
	if res1.Data["cached"] != false {
		t.Fatalf("first call should not be cached")
	}
	if got := atomic.LoadInt32(&fileSvc.getFileHits); got != 1 {
		t.Fatalf("first call expected 1 GetFile, got %d", got)
	}
	// Second call: idempotent probe should hit; no second GetFile.
	res2, err := tool.Execute(ctx, mustMarshal(t, KBGetFileInput{KnowledgeID: "k-1"}))
	if err != nil || !res2.Success {
		t.Fatalf("second Execute failed: %v %+v", err, res2)
	}
	if res2.Data["cached"] != true {
		t.Fatalf("second call should be cached=true")
	}
	if got := atomic.LoadInt32(&fileSvc.getFileHits); got != 1 {
		t.Fatalf("second call must not re-download, got %d GetFile calls total", got)
	}
	// Path stays the same.
	if res1.Data["path"] != res2.Data["path"] {
		t.Fatalf("path drifted between calls: %s vs %s", res1.Data["path"], res2.Data["path"])
	}
	_ = store
}

func TestKBGetFile_MissingSessionID_Refused(t *testing.T) {
	tool, _, _ := newKBFileTestTool(t)
	res, _ := tool.Execute(context.Background(), mustMarshal(t, KBGetFileInput{
		KnowledgeID: "k-1",
	}))
	if res.Success {
		t.Fatalf("expected failure when no session ID in ctx")
	}
	if !kbFileContains(res.Error, "no session ID") {
		t.Fatalf("expected 'no session ID' error, got %q", res.Error)
	}
}

func TestKBGetFile_EmptyKnowledgeID_Refused(t *testing.T) {
	tool, _, _ := newKBFileTestTool(t)
	res, _ := tool.Execute(withSessionKBFile(context.Background()), mustMarshal(t, KBGetFileInput{
		KnowledgeID: "",
	}))
	if res.Success {
		t.Fatalf("expected failure for empty knowledge_id")
	}
	if !kbFileContains(res.Error, "knowledge_id is required") {
		t.Fatalf("expected 'knowledge_id is required' error, got %q", res.Error)
	}
}

func TestKBGetFile_ResolverFailure_FallbackDefault(t *testing.T) {
	tool, store, defaultSvc := newKBFileTestTool(t)
	tool.knowledgeService = &stubKBFileKnowledgeService{
		getByIDOnly: func(ctx context.Context, id string) (*types.Knowledge, error) {
			return kbFileMakeKnowledge("k-1", "kb-A",
				withKnowledgeFileSize(0)), nil // disable cache hit
		},
	}
	// Resolver returns an error; shared helper falls back to the
	// tenant-default fileService (which is defaultSvc here).
	tool.storageResolver = &stubResolver{
		resolve: func(ctx context.Context, tnt *types.Tenant, backendID, provider, baseDir string) (
			interfaces.FileService, string, error) {
			return nil, "", errors.New("resolver failed")
		},
	}
	atomic.StoreInt32(&defaultSvc.getFileHits, 0)
	res, err := tool.Execute(withSessionKBFile(context.Background()), mustMarshal(t, KBGetFileInput{
		KnowledgeID: "k-1",
	}))
	if err != nil || !res.Success {
		t.Fatalf("Execute failed: %v %+v", err, res)
	}
	if got := atomic.LoadInt32(&defaultSvc.getFileHits); got != 1 {
		t.Fatalf("expected default FileService to be used after resolver failure, got %d calls", got)
	}
	if !store.has("test-session", res.Data["path"].(string)) {
		t.Fatalf("file not staged via fallback path")
	}
}

func TestKBGetFile_PathDeterministic(t *testing.T) {
	tool, _, _ := newKBFileTestTool(t)
	tool.knowledgeService = &stubKBFileKnowledgeService{
		getByIDOnly: func(ctx context.Context, id string) (*types.Knowledge, error) {
			return kbFileMakeKnowledge("k-1", "kb-A"), nil
		},
	}
	res1, _ := tool.Execute(withSessionKBFile(context.Background()), mustMarshal(t, KBGetFileInput{
		KnowledgeID: "k-1",
		Filename:    "same-name.pdf",
	}))
	// Drop the cached entry so the second call forces a fresh path
	// computation (the tool does not mutate paths between calls, but
	// we want to assert the path is sha-stable across calls).
	tool.knowledgeService = &stubKBFileKnowledgeService{
		getByIDOnly: func(ctx context.Context, id string) (*types.Knowledge, error) {
			return kbFileMakeKnowledge("k-1", "kb-A"), nil
		},
	}
	res2, _ := tool.Execute(withSessionKBFile(context.Background()), mustMarshal(t, KBGetFileInput{
		KnowledgeID: "k-1",
		Filename:    "same-name.pdf",
	}))
	if res1.Data["path"] != res2.Data["path"] {
		t.Fatalf("path must be deterministic, got %s vs %s", res1.Data["path"], res2.Data["path"])
	}
}

// kbFileContains is a tiny substring helper that avoids pulling in strings just
// for these tests.
func kbFileContains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// endsWith checks the trailing suffix; written inline to keep this file's
// imports tight.
func endsWith(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}

// Reference sandbox package so the test compiles when the import block is
// pruned during copy-paste. Not actually used at runtime.
var _ = sandbox.RemoteEntryFile
