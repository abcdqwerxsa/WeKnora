package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubChunkService implements interfaces.ChunkService by embedding it as a
// nil field (so unimplemented methods panic) and overriding only
// GetChunkByID — the only method the tool calls.
type stubChunkService struct {
	interfaces.ChunkService
	getByID func(ctx context.Context, id string) (*types.Chunk, error)
}

func (s *stubChunkService) GetChunkByID(ctx context.Context, id string) (*types.Chunk, error) {
	return s.getByID(ctx, id)
}

// stubFileService implements interfaces.FileService. Tracks GetFile call
// counts so idempotency tests can assert the second call did not download.
type stubFileService struct {
	interfaces.FileService
	getFile     func(ctx context.Context, path string) (io.ReadCloser, error)
	getFileHits int32
}

func (s *stubFileService) GetFile(ctx context.Context, path string) (io.ReadCloser, error) {
	atomic.AddInt32(&s.getFileHits, 1)
	return s.getFile(ctx, path)
}

// stubResolver implements interfaces.StorageBackendResolver. Returning an
// error simulates the "resolver failed" path; returning the same FileService
// instance is the success path.
type stubResolver struct {
	interfaces.StorageBackendResolver
	resolve func(ctx context.Context, t *types.Tenant, backendID, provider, baseDir string) (
		interfaces.FileService, string, error)
}

func (s *stubResolver) ResolveFileService(
	ctx context.Context, t *types.Tenant, backendID, provider, baseDir string,
) (interfaces.FileService, string, error) {
	return s.resolve(ctx, t, backendID, provider, baseDir)
}

// stubTenantService implements interfaces.TenantService.
type stubTenantService struct {
	interfaces.TenantService
	getByID func(ctx context.Context, id uint64) (*types.Tenant, error)
}

func (s *stubTenantService) GetTenantByID(ctx context.Context, id uint64) (*types.Tenant, error) {
	return s.getByID(ctx, id)
}

// stubKBKnowledgeService implements interfaces.KnowledgeService. The default
// fixture returns an error so the sidecar's document_title field stays
// empty — tests that want a populated sidecar override getByID.
type stubKBKnowledgeService struct {
	interfaces.KnowledgeService
	getByID func(ctx context.Context, id string) (*types.Knowledge, error)
}

func (s *stubKBKnowledgeService) GetKnowledgeByID(ctx context.Context, id string) (*types.Knowledge, error) {
	return s.getByID(ctx, id)
}

// stubKBKnowledgeBaseService implements interfaces.KnowledgeBaseService. The
// default fixture returns an error so the sidecar's kb_name field stays
// empty — tests that want a populated sidecar override getByID.
type stubKBKnowledgeBaseService struct {
	interfaces.KnowledgeBaseService
	getByID func(ctx context.Context, id string) (*types.KnowledgeBase, error)
}

func (s *stubKBKnowledgeBaseService) GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error) {
	return s.getByID(ctx, id)
}

// memFileStore is an in-memory implementation of sandbox.SessionFileStore.
// It embeds sandbox.SessionFileStore as a nil field so unimplemented
// methods panic if any are reached; tests only exercise the four methods
// the tool actually calls.
type memFileStore struct {
	sandbox.SessionFileStore
	mu    sync.Mutex
	files map[string][]byte
	dirs  map[string]bool
}

func newMemFileStore() *memFileStore {
	return &memFileStore{files: map[string][]byte{}, dirs: map[string]bool{}}
}

func (m *memFileStore) EnsureSessionDir(ctx context.Context, sessionID, dir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dirs[sessionID+":"+dir] = true
	return nil
}

func (m *memFileStore) StatSessionFile(ctx context.Context, sessionID, path string) (*sandbox.RemoteStatEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[sessionID+":"+path]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &sandbox.RemoteStatEntry{Type: sandbox.RemoteEntryFile, Size: int64(len(data))}, nil
}

func (m *memFileStore) ReadSessionFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[sessionID+":"+path]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return append([]byte(nil), data...), nil
}

func (m *memFileStore) WriteSessionWorkspaceFile(ctx context.Context, sessionID, path string, content []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[sessionID+":"+path] = append([]byte(nil), content...)
	return nil
}

func (m *memFileStore) has(sessionID, path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.files[sessionID+":"+path]
	return ok
}

// withSession returns a context that resolveSessionID recognizes as
// belonging to "test-session". It uses the same WithSessionID helper the
// production code path uses, so the test exercises the real key.
func withSession(ctx context.Context) context.Context {
	return types.WithSessionID(ctx, "test-session")
}

// pngMagic returns the 8-byte PNG magic prefix.
func pngMagic() []byte {
	return []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
}

// jpegMagic returns the 3-byte JPEG SOI prefix.
func jpegMagic() []byte {
	return []byte{0xFF, 0xD8, 0xFF}
}

// makeChunk builds a Chunk with sane defaults for tests.
func makeChunk(id, kbID string, opts ...func(*types.Chunk)) *types.Chunk {
	c := &types.Chunk{
		ID:              id,
		KnowledgeBaseID: kbID,
		TenantID:        7,
		ChunkType:       types.ChunkTypeText,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func withImageURL(url string) func(*types.Chunk) {
	return func(c *types.Chunk) {
		infos, _ := json.Marshal([]types.ImageInfo{{URL: url}})
		c.ImageInfo = string(infos)
	}
}

func withChunkType(t types.ChunkType) func(*types.Chunk) {
	return func(c *types.Chunk) { c.ChunkType = t }
}

func withParentID(id string) func(*types.Chunk) {
	return func(c *types.Chunk) { c.ParentChunkID = id }
}

func withKnowledgeID(id string) func(*types.Chunk) {
	return func(c *types.Chunk) { c.KnowledgeID = id }
}

func withImageCaption(caption string) func(*types.Chunk) {
	return func(c *types.Chunk) {
		infos, _ := json.Marshal([]types.ImageInfo{{Caption: caption}})
		c.ImageInfo = string(infos)
	}
}

// newTestTool wires a KBGetImageTool with stub deps that satisfy the
// common success path. Tests override individual deps on the returned
// struct to exercise error paths.
func newTestTool(t *testing.T) (*KBGetImageTool, *memFileStore, *stubFileService) {
	t.Helper()
	store := newMemFileStore()
	fileSvc := &stubFileService{
		getFile: func(ctx context.Context, p string) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(pngMagic())), nil
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
	chunks := &stubChunkService{
		getByID: func(ctx context.Context, id string) (*types.Chunk, error) {
			return nil, errors.New("not implemented in default stub")
		},
	}
	targets := types.SearchTargets{
		{KnowledgeBaseID: "kb-A", TenantID: 7},
		{KnowledgeBaseID: "kb-B", TenantID: 7},
	}
	// Default Knowledge / KB fixtures return errors so the sidecar's
	// document_title / kb_name fields are absent. Tests that care about
	// the sidecar content override these on the returned tool struct.
	knowledge := &stubKBKnowledgeService{
		getByID: func(ctx context.Context, id string) (*types.Knowledge, error) {
			return nil, errors.New("knowledge stub: not implemented")
		},
	}
	kbases := &stubKBKnowledgeBaseService{
		getByID: func(ctx context.Context, id string) (*types.KnowledgeBase, error) {
			return nil, errors.New("kb stub: not implemented")
		},
	}
	tool := NewKBGetImageTool(fileSvc, chunks, knowledge, kbases, resolver, tenants, targets, store)
	return tool, store, fileSvc
}

func TestKBGetImage_SuccessPath_ResourceSchemeURL(t *testing.T) {
	// Internal "resource://" URLs are opaque handles that resolve through
	// the ResourceCatalog; the FileService returned by the storage resolver
	// wraps the physical backend with NewResourceCatalogFileService, so
	// GetFile("resource://...") succeeds without the agent (or the model)
	// inspecting the URL. The agent prompt must NOT pre-judge feasibility
	// based on the scheme.
	tool, store, fileSvc := newTestTool(t)
	tool.chunkService.(*stubChunkService).getByID = func(
		ctx context.Context, id string,
	) (*types.Chunk, error) {
		return makeChunk("c1", "kb-A",
			withImageURL("resource://AbCdEfGhIjKlMnOpQrStUv")), nil
	}
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 256)...)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		// In production this would route through ResourceCatalogFileService
		// which converts resource:// → physical path before calling inner
		// GetFile. The mock short-circuits that, but the URL flow in
		// kb_get_image still has to handle the resource:// scheme without
		// crashing in ParseProviderScheme / ParseStorageBackendPath.
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	res, err := tool.Execute(withSession(context.Background()), json.RawMessage(`{"chunk_id":"c1"}`))
	if err != nil || !res.Success {
		t.Fatalf("Execute failed: %v %s", err, res.Error)
	}
	// urlExt("resource://AbCdEfGh...") returns "" — the URL has no file
	// extension. Sniffed magic byte gives the .png extension. Path must
	// be /workspace/output/images/kb-A/c1.png, NOT c1.bin.
	data := res.Data
	if got := data["path"]; got != "/workspace/output/images/kb-A/c1.png" {
		t.Errorf("path = %v, want /workspace/output/images/kb-A/c1.png", got)
	}
	if got := data["mime"]; got != "png" {
		t.Errorf("mime = %v, want png", got)
	}
	if !store.has("test-session", "/workspace/output/images/kb-A/c1.png") {
		t.Error("expected bytes at the sniffed-ext path, not at .bin placeholder")
	}
}

func TestKBGetImage_SuccessPath_TextChunkWithImage(t *testing.T) {
	tool, store, fileSvc := newTestTool(t)
	tool.chunkService.(*stubChunkService).getByID = func(
		ctx context.Context, id string,
	) (*types.Chunk, error) {
		return makeChunk("c1", "kb-A", withImageURL("minio://bucket/c1.png")), nil
	}
	// Override GetFile to return real PNG bytes (not just magic) so size is non-trivial.
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 1024)...)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	res, err := tool.Execute(withSession(context.Background()), json.RawMessage(`{"chunk_id":"c1"}`))
	if err != nil {
		t.Fatalf("Execute returned err: %v", err)
	}
	if !res.Success {
		t.Fatalf("Execute failed: %s", res.Error)
	}
	data := res.Data
	wantPath := "/workspace/output/images/kb-A/c1.png"
	if got := data["path"]; got != wantPath {
		t.Errorf("path = %v, want %s", got, wantPath)
	}
	if got := data["size"]; got != int64(len(realBytes)) {
		t.Errorf("size = %v (%T), want %d", got, got, len(realBytes))
	}
	if got := data["cached"]; got != false {
		t.Errorf("cached = %v, want false", got)
	}
	if got := data["mime"]; got != "png" {
		t.Errorf("mime = %v, want png", got)
	}
	if !store.has("test-session", wantPath) {
		t.Errorf("expected bytes at %s in sandbox stub", wantPath)
	}
	if got := atomic.LoadInt32(&fileSvc.getFileHits); got != 1 {
		t.Errorf("GetFile hits = %d, want 1", got)
	}
}

func TestKBGetImage_SuccessPath_ImageOCRResolvesParent(t *testing.T) {
	tool, store, _ := newTestTool(t)
	tool.chunkService.(*stubChunkService).getByID = func(
		ctx context.Context, id string,
	) (*types.Chunk, error) {
		switch id {
		case "ocr-1":
			return makeChunk("ocr-1", "kb-A",
				withChunkType(types.ChunkTypeImageOCR),
				withParentID("p1")), nil
		case "p1":
			return makeChunk("p1", "kb-A", withImageURL("minio://bucket/p1.jpg")), nil
		}
		return nil, errors.New("unexpected lookup")
	}
	// Override default GetFile to return JPEG magic.
	jpegBytes := append(jpegMagic(), bytes.Repeat([]byte{0x00}, 512)...)
	tool.fileService.(*stubFileService).getFile = func(
		ctx context.Context, p string,
	) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(jpegBytes)), nil
	}

	res, err := tool.Execute(withSession(context.Background()), json.RawMessage(`{"chunk_id":"ocr-1"}`))
	if err != nil || !res.Success {
		t.Fatalf("Execute failed: %v %s", err, res.Error)
	}
	wantPath := "/workspace/output/images/kb-A/ocr-1.jpg"
	if got := res.Data["path"]; got != wantPath {
		t.Errorf("path = %v, want %s", got, wantPath)
	}
	if !store.has("test-session", wantPath) {
		t.Errorf("expected bytes at %s in sandbox stub", wantPath)
	}
}

func TestKBGetImage_ChunkNotFound_NoLeak(t *testing.T) {
	tool, store, _ := newTestTool(t)
	tool.chunkService.(*stubChunkService).getByID = func(
		ctx context.Context, id string,
	) (*types.Chunk, error) {
		return nil, errors.New("chunk not found in storage")
	}

	res, _ := tool.Execute(withSession(context.Background()), json.RawMessage(`{"chunk_id":"x"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected not-found error")
	}
	want := "chunk not found: x"
	if res.Error != want {
		t.Errorf("Error = %q, want %q (no internal detail)", res.Error, want)
	}
	if len(store.files) != 0 {
		t.Errorf("sandbox should be empty on not-found; has %d files", len(store.files))
	}
}

func TestKBGetImage_CrossTenant_GenericNotFound(t *testing.T) {
	tool, _, _ := newTestTool(t)
	tool.chunkService.(*stubChunkService).getByID = func(
		ctx context.Context, id string,
	) (*types.Chunk, error) {
		// Chunk belongs to a KB NOT in search targets.
		return makeChunk("c1", "kb-OUT-OF-SCOPE", withImageURL("minio://bucket/c1.png")), nil
	}

	res, _ := tool.Execute(withSession(context.Background()), json.RawMessage(`{"chunk_id":"c1"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected out-of-scope refusal")
	}
	want := "chunk not found: c1"
	if res.Error != want {
		t.Errorf("Error = %q, want %q (cross-tenant must look identical to 404)", res.Error, want)
	}
}

func TestKBGetImage_ImageOCR_NoParent(t *testing.T) {
	tool, _, _ := newTestTool(t)
	tool.chunkService.(*stubChunkService).getByID = func(
		ctx context.Context, id string,
	) (*types.Chunk, error) {
		return makeChunk("ocr-1", "kb-A",
			withChunkType(types.ChunkTypeImageOCR),
			withParentID("")), nil
	}

	res, _ := tool.Execute(withSession(context.Background()), json.RawMessage(`{"chunk_id":"ocr-1"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected parent-resolution error")
	}
	if !contains(res.Error, "no resolvable parent image") {
		t.Errorf("Error = %q, want substring 'no resolvable parent image'", res.Error)
	}
}

func TestKBGetImage_TextChunkNoImage(t *testing.T) {
	tool, _, _ := newTestTool(t)
	tool.chunkService.(*stubChunkService).getByID = func(
		ctx context.Context, id string,
	) (*types.Chunk, error) {
		// Plain text chunk with no ImageInfo.
		return makeChunk("c-text", "kb-A"), nil
	}

	res, _ := tool.Execute(withSession(context.Background()), json.RawMessage(`{"chunk_id":"c-text"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected not-an-image-chunk error")
	}
	if !contains(res.Error, "is not an image chunk") {
		t.Errorf("Error = %q, want substring 'is not an image chunk'", res.Error)
	}
}

func TestKBGetImage_Oversize_Refused(t *testing.T) {
	tool, store, fileSvc := newTestTool(t)
	tool.chunkService.(*stubChunkService).getByID = func(
		ctx context.Context, id string,
	) (*types.Chunk, error) {
		return makeChunk("c1", "kb-A", withImageURL("minio://bucket/c1.png")), nil
	}
	huge := bytes.Repeat([]byte{0x00}, maxKBImageBytes+1)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(huge)), nil
	}

	res, _ := tool.Execute(withSession(context.Background()), json.RawMessage(`{"chunk_id":"c1"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected oversize refusal")
	}
	if !contains(res.Error, "over the") || !contains(res.Error, "byte cap") {
		t.Errorf("Error = %q, want oversize cap message", res.Error)
	}
	if len(store.files) != 0 {
		t.Errorf("sandbox must remain empty after refusal; has %d files", len(store.files))
	}
}

func TestKBGetImage_MagicSniff_FallbackURL(t *testing.T) {
	tool, store, fileSvc := newTestTool(t)
	tool.chunkService.(*stubChunkService).getByID = func(
		ctx context.Context, id string,
	) (*types.Chunk, error) {
		// ImageInfo.OriginalURL has .tiff; bytes are random so magic fails.
		return makeChunk("c1", "kb-A",
			withImageURL("minio://bucket/c1.tiff")), nil
	}
	noise := bytes.Repeat([]byte{0xAB}, 256)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(noise)), nil
	}

	res, _ := tool.Execute(withSession(context.Background()), json.RawMessage(`{"chunk_id":"c1"}`))
	if !res.Success {
		t.Fatalf("Execute failed: %s", res.Error)
	}
	if got := res.Data["path"]; got != "/workspace/output/images/kb-A/c1.tiff" {
		t.Errorf("path = %v, want .tiff fallback", got)
	}
	if !store.has("test-session", "/workspace/output/images/kb-A/c1.tiff") {
		t.Error("expected .tiff file staged")
	}
}

func TestKBGetImage_MagicSniff_NoFallback(t *testing.T) {
	tool, _, fileSvc := newTestTool(t)
	tool.chunkService.(*stubChunkService).getByID = func(
		ctx context.Context, id string,
	) (*types.Chunk, error) {
		// ImageInfo URL has no extension either.
		return makeChunk("c1", "kb-A",
			withImageURL("minio://bucket/c1")), nil
	}
	noise := bytes.Repeat([]byte{0xAB}, 256)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(noise)), nil
	}

	res, _ := tool.Execute(withSession(context.Background()), json.RawMessage(`{"chunk_id":"c1"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected unrecognized format error")
	}
	if !contains(res.Error, "unrecognized image format") {
		t.Errorf("Error = %q, want unrecognized format message", res.Error)
	}
}

func TestKBGetImage_Idempotency_SecondCallCached(t *testing.T) {
	tool, store, fileSvc := newTestTool(t)
	tool.chunkService.(*stubChunkService).getByID = func(
		ctx context.Context, id string,
	) (*types.Chunk, error) {
		return makeChunk("c1", "kb-A", withImageURL("minio://bucket/c1.png")), nil
	}
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 256)...)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	ctx := withSession(context.Background())
	first, _ := tool.Execute(ctx, json.RawMessage(`{"chunk_id":"c1"}`))
	if !first.Success {
		t.Fatalf("first call failed: %s", first.Error)
	}
	if got := first.Data["cached"]; got != false {
		t.Errorf("first call cached = %v, want false", got)
	}

	second, _ := tool.Execute(ctx, json.RawMessage(`{"chunk_id":"c1"}`))
	if !second.Success {
		t.Fatalf("second call failed: %s", second.Error)
	}
	if got := second.Data["cached"]; got != true {
		t.Errorf("second call cached = %v, want true", got)
	}
	if got := atomic.LoadInt32(&fileSvc.getFileHits); got != 1 {
		t.Errorf("GetFile hits = %d, want 1 (second call must not re-download)", got)
	}
	if !store.has("test-session", "/workspace/output/images/kb-A/c1.png") {
		t.Error("expected bytes at cached path")
	}
}

func TestKBGetImage_MissingSessionID_Refused(t *testing.T) {
	tool, _, _ := newTestTool(t)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"chunk_id":"c1"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected missing-session refusal")
	}
	if !contains(res.Error, "no session ID") {
		t.Errorf("Error = %q, want missing-session message", res.Error)
	}
}

func TestKBGetImage_ResolverFailure_FallbackDefault(t *testing.T) {
	tool, store, fileSvc := newTestTool(t)
	tool.chunkService.(*stubChunkService).getByID = func(
		ctx context.Context, id string,
	) (*types.Chunk, error) {
		return makeChunk("c1", "kb-A", withImageURL("minio://bucket/c1.png")), nil
	}
	// Resolver returns an error; tool must fall back to t.fileService.
	tool.storageResolver.(*stubResolver).resolve = func(
		ctx context.Context, tnt *types.Tenant, backendID, provider, baseDir string,
	) (interfaces.FileService, string, error) {
		return nil, "", errors.New("resolver boom")
	}
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 256)...)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	res, _ := tool.Execute(withSession(context.Background()), json.RawMessage(`{"chunk_id":"c1"}`))
	if !res.Success {
		t.Fatalf("Execute failed: %s", res.Error)
	}
	if !store.has("test-session", "/workspace/output/images/kb-A/c1.png") {
		t.Error("expected bytes via fallback file service")
	}
}

func TestKBGetImage_EmptyChunkID_Refused(t *testing.T) {
	tool, _, _ := newTestTool(t)
	res, _ := tool.Execute(withSession(context.Background()), json.RawMessage(`{}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected missing-chunk_id refusal")
	}
	if !contains(res.Error, "chunk_id is required") {
		t.Errorf("Error = %q, want required-field message", res.Error)
	}
}

func TestKBGetImage_URLFallback_ResourceScheme(t *testing.T) {
	// The agent sometimes passes an ImageInfo URL (resource://...) where
	// chunk_id is expected — kb_get_image must accept that, fetch bytes,
	// and return success instead of "chunk not found".
	tool, store, fileSvc := newTestTool(t)
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 256)...)
	fileSvc.getFile = func(ctx context.Context, path string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	res, err := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"chunk_id":"resource://025wcMnbr8OcGM-gC6-QNQ"}`))
	if err != nil {
		t.Fatalf("Execute returned err: %v", err)
	}
	if !res.Success {
		t.Fatalf("Execute failed: %s", res.Error)
	}
	// URL fallback stages under /workspace/output/images/by_url/<handle>.<ext>.
	wantPath := "/workspace/output/images/by_url/025wcMnbr8OcGM-gC6-QNQ.png"
	if got := res.Data["path"]; got != wantPath {
		t.Errorf("path = %v, want %s", got, wantPath)
	}
	if got := res.Data["kb_id"]; got != "by_url" {
		t.Errorf("kb_id = %v, want by_url (synthetic bucket)", got)
	}
	if got := res.Data["cached"]; got != false {
		t.Errorf("cached = %v, want false", got)
	}
	if !store.has("test-session", wantPath) {
		t.Errorf("expected bytes at %s in sandbox stub", wantPath)
	}
	// chunk_id slot carries the original URL so the agent can echo it back.
	if got := res.Data["chunk_id"]; got != "resource://025wcMnbr8OcGM-gC6-QNQ" {
		t.Errorf("chunk_id = %v, want the original URL preserved", got)
	}
}

func TestKBGetImage_URLFallback_ProviderScheme(t *testing.T) {
	tool, store, fileSvc := newTestTool(t)
	realBytes := append(jpegMagic(), bytes.Repeat([]byte{0x00}, 128)...)
	fileSvc.getFile = func(ctx context.Context, path string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	res, _ := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"chunk_id":"provider://bucket/path/cert.jpg"}`))
	if !res.Success {
		t.Fatalf("Execute failed: %s", res.Error)
	}
	// provider:// falls back to basename of the path part.
	wantPath := "/workspace/output/images/by_url/cert.jpg"
	if got := res.Data["path"]; got != wantPath {
		t.Errorf("path = %v, want %s", got, wantPath)
	}
	if !store.has("test-session", wantPath) {
		t.Errorf("expected bytes at %s", wantPath)
	}
}

func TestKBGetImage_URLFallback_IdempotentSecondCall(t *testing.T) {
	tool, _, fileSvc := newTestTool(t)
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 64)...)
	fileSvc.getFile = func(ctx context.Context, path string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	ctx := withSession(context.Background())
	url := "resource://abc-handle-with-png-ext"
	first, _ := tool.Execute(ctx, json.RawMessage(`{"chunk_id":"`+url+`"}`))
	if !first.Success {
		t.Fatalf("first call failed: %s", first.Error)
	}
	if got := first.Data["cached"]; got != false {
		t.Errorf("first call cached = %v, want false", got)
	}
	second, _ := tool.Execute(ctx, json.RawMessage(`{"chunk_id":"`+url+`"}`))
	if !second.Success {
		t.Fatalf("second call failed: %s", second.Error)
	}
	if got := second.Data["cached"]; got != true {
		t.Errorf("second call cached = %v, want true", got)
	}
	if got := atomic.LoadInt32(&fileSvc.getFileHits); got != 1 {
		t.Errorf("GetFile hits = %d, want 1 (URL fallback must not re-download)", got)
	}
}

func TestKBGetImage_ModelHandle_ResDoubleSlash(t *testing.T) {
	// When the modelcontext layer fails to translate `res://NNNN` →
	// canonical URL, the unresolved handle reaches the tool. We surface a
	// specific error so the agent doesn't waste a turn chasing the chunk
	// table for `res::0014`.
	tool, _, _ := newTestTool(t)
	res, _ := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"chunk_id":"res://0014"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected unresolved-model-handle error")
	}
	if !contains(res.Error, "res:// or res:: prefix") {
		t.Errorf("Error = %q, want message pointing at model handle confusion", res.Error)
	}
}

func TestKBGetImage_ModelHandle_ResDoubleColon(t *testing.T) {
	// `res::NNNN` (double colon, sometimes emitted by LLMs that confuse
	// URL schemes) must hit the same trap.
	tool, _, _ := newTestTool(t)
	res, _ := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"chunk_id":"res::0014"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected unresolved-model-handle error")
	}
	if !contains(res.Error, "res:// or res:: prefix") {
		t.Errorf("Error = %q, want message pointing at model handle confusion", res.Error)
	}
}

func TestKBGetImage_WikiSlugSummary(t *testing.T) {
	// `summary/<uuid>` is a wiki page slug — belongs to wiki_read_page,
	// not kb_get_image. Catching this here spares the agent a "chunk
	// not found" loop on a tool-selection mistake.
	tool, _, _ := newTestTool(t)
	res, _ := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"chunk_id":"summary/1fbaf976-4491-4026-9df0-4d22459a1ab5"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected wiki-slug routing error")
	}
	if !contains(res.Error, "wiki page slug") {
		t.Errorf("Error = %q, want message pointing at wiki slug", res.Error)
	}
	if !contains(res.Error, "wiki_read_page") {
		t.Errorf("Error = %q, want message pointing at wiki_read_page tool", res.Error)
	}
}

// contains is a tiny stdlib-free substring check (strings.Contains requires
// the strings import; this avoids adding the dep at the top of the file
// just for tests, since the production code uses contains elsewhere).
func contains(haystack, needle string) bool {
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

// TestKBGetImage_Sidecar_PopulatedOnSuccess asserts the .txt sidecar
// carries KB / Document / Chunk / Caption / Content fields once KB and
// Knowledge lookups succeed. The agent downstream uses this sidecar to
// derive a caption without re-querying the search tools.
func TestKBGetImage_Sidecar_PopulatedOnSuccess(t *testing.T) {
	tool, store, fileSvc := newTestTool(t)
	tool.chunkService.(*stubChunkService).getByID = func(
		ctx context.Context, id string,
	) (*types.Chunk, error) {
		// ImageInfo must carry both URL and Caption; the tool needs URL for
		// byte-fetch and uses Caption for the sidecar caption field.
		infos, _ := json.Marshal([]types.ImageInfo{{
			URL:     "minio://bucket/c1.png",
			Caption: "系统架构总览",
		}})
		c := makeChunk("c1", "kb-A", withKnowledgeID("doc-1"))
		c.ImageInfo = string(infos)
		c.Content = "系统整体采用微服务架构。"
		return c, nil
	}
	tool.knowledgeService.(*stubKBKnowledgeService).getByID = func(
		ctx context.Context, id string,
	) (*types.Knowledge, error) {
		return &types.Knowledge{ID: id, Title: "2026 Q1 产品手册", KnowledgeBaseID: "kb-A"}, nil
	}
	tool.knowledgeBaseService.(*stubKBKnowledgeBaseService).getByID = func(
		ctx context.Context, id string,
	) (*types.KnowledgeBase, error) {
		return &types.KnowledgeBase{ID: id, Name: "产品手册库"}, nil
	}
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 256)...)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	res, err := tool.Execute(withSession(context.Background()), json.RawMessage(`{"chunk_id":"c1"}`))
	if err != nil || !res.Success {
		t.Fatalf("Execute failed: %v %s", err, res.Error)
	}

	wantDescPath := "/workspace/output/images/kb-A/c1.txt"
	if got := res.Data["description_path"]; got != wantDescPath {
		t.Errorf("description_path = %v, want %s", got, wantDescPath)
	}
	if !store.has("test-session", wantDescPath) {
		t.Fatalf("expected sidecar at %s", wantDescPath)
	}
	body, _ := store.ReadSessionFile(context.Background(), "test-session", wantDescPath)
	checks := []string{
		"source: kb",
		"kb_id: kb-A",
		"kb_name: 产品手册库",
		"document_id: doc-1",
		"document_title: 2026 Q1 产品手册",
		"chunk_id: c1",
		"caption: 系统架构总览",
		"content: |",
		"系统整体采用微服务架构。",
		"image_url: minio://bucket/c1.png",
	}
	for _, want := range checks {
		if !contains(string(body), want) {
			t.Errorf("sidecar missing %q; body=%q", want, body)
		}
	}
}

// TestKBGetImage_Sidecar_KBLookupFailure_StillWrites verifies the sidecar
// is still written when KB / Knowledge lookups fail (the sidecar is
// enhancement-layer metadata, not data-integrity-required).
func TestKBGetImage_Sidecar_KBLookupFailure_StillWrites(t *testing.T) {
	tool, store, fileSvc := newTestTool(t)
	tool.chunkService.(*stubChunkService).getByID = func(
		ctx context.Context, id string,
	) (*types.Chunk, error) {
		return makeChunk("c1", "kb-A",
			withKnowledgeID("doc-1"),
			withImageURL("minio://bucket/c1.png")), nil
	}
	// Default stubs already return errors for KB / Knowledge — that's the
	// failure mode this test exercises.
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 256)...)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	res, err := tool.Execute(withSession(context.Background()), json.RawMessage(`{"chunk_id":"c1"}`))
	if err != nil || !res.Success {
		t.Fatalf("Execute failed: %v %s", err, res.Error)
	}

	wantDescPath := "/workspace/output/images/kb-A/c1.txt"
	if !store.has("test-session", wantDescPath) {
		t.Fatalf("expected sidecar at %s even with KB/Knowledge lookup failures", wantDescPath)
	}
	body, _ := store.ReadSessionFile(context.Background(), "test-session", wantDescPath)
	// kb_id and document_id are still required fields; kb_name / document_title
	// must be absent because lookups failed.
	if contains(string(body), "kb_name:") {
		t.Errorf("expected kb_name to be absent on lookup failure; body=%q", body)
	}
	if contains(string(body), "document_title:") {
		t.Errorf("expected document_title to be absent on lookup failure; body=%q", body)
	}
	if !contains(string(body), "kb_id: kb-A") {
		t.Errorf("kb_id must still be present; body=%q", body)
	}
}

// TestKBGetImage_Sidecar_URLFallback_StillWrites verifies the URL fallback
// path (no KB / chunk context) also produces a sidecar — even if it's a
// thin "by_url" record.
func TestKBGetImage_Sidecar_URLFallback_StillWrites(t *testing.T) {
	tool, store, fileSvc := newTestTool(t)
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 128)...)
	fileSvc.getFile = func(ctx context.Context, path string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	url := "resource://025wcMnbr8OcGM-gC6-QNQ"
	res, err := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"chunk_id":"`+url+`"}`))
	if err != nil || !res.Success {
		t.Fatalf("Execute failed: %v %s", err, res.Error)
	}

	wantDescPath := "/workspace/output/images/by_url/025wcMnbr8OcGM-gC6-QNQ.txt"
	if got := res.Data["description_path"]; got != wantDescPath {
		t.Errorf("description_path = %v, want %s", got, wantDescPath)
	}
	if !store.has("test-session", wantDescPath) {
		t.Fatalf("expected sidecar at %s", wantDescPath)
	}
	body, _ := store.ReadSessionFile(context.Background(), "test-session", wantDescPath)
	if !contains(string(body), "source: by_url") {
		t.Errorf("URL fallback sidecar must declare source=by_url; body=%q", body)
	}
	if !contains(string(body), url) {
		t.Errorf("URL fallback sidecar must include the original URL; body=%q", body)
	}
}

// TestKBGetImage_Sidecar_CacheHitReuses asserts the second call with the
// same chunk_id reports the same description_path (idempotent sidecar).
func TestKBGetImage_Sidecar_CacheHitReuses(t *testing.T) {
	tool, _, fileSvc := newTestTool(t)
	tool.chunkService.(*stubChunkService).getByID = func(
		ctx context.Context, id string,
	) (*types.Chunk, error) {
		return makeChunk("c1", "kb-A", withImageURL("minio://bucket/c1.png")), nil
	}
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 256)...)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	ctx := withSession(context.Background())
	first, _ := tool.Execute(ctx, json.RawMessage(`{"chunk_id":"c1"}`))
	if !first.Success {
		t.Fatalf("first call failed: %s", first.Error)
	}
	second, _ := tool.Execute(ctx, json.RawMessage(`{"chunk_id":"c1"}`))
	if !second.Success {
		t.Fatalf("second call failed: %s", second.Error)
	}
	if first.Data["description_path"] != second.Data["description_path"] {
		t.Errorf("description_path drift: first=%v second=%v",
			first.Data["description_path"], second.Data["description_path"])
	}
}

// Compile-time sanity: ensure the tool satisfies the types.Tool interface
// so a missing-method regression fails the build instead of going silent.
var _ types.Tool = (*KBGetImageTool)(nil)