package imagefetch

import (
	"bytes"
	"context"
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

// stubFileService tracks call counts so idempotency tests can assert the
// second call did not re-download.
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
// error simulates "resolver failed"; returning the same FileService instance
// is the success path.
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

// memFileStore is an in-memory sandbox.SessionFileStore for tests. Only the
// methods the helper actually calls are implemented.
type memFileStore struct {
	sandbox.SessionFileStore
	mu    sync.Mutex
	files map[string][]byte
}

func newMemFileStore() *memFileStore {
	return &memFileStore{files: map[string][]byte{}}
}

func (m *memFileStore) EnsureSessionDir(ctx context.Context, sessionID, dir string) error {
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

func (m *memFileStore) WriteSessionWorkspaceFile(ctx context.Context, sessionID, path string, content []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[sessionID+":"+path] = append([]byte(nil), content...)
	return nil
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

func (m *memFileStore) has(sessionID, path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.files[sessionID+":"+path]
	return ok
}

func pngMagic() []byte {
	return []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
}

func jpegMagic() []byte {
	return []byte{0xFF, 0xD8, 0xFF}
}

func withSession(ctx context.Context, sessionID string) context.Context {
	return types.WithSessionID(ctx, sessionID)
}

// makeReq returns a StageRequest with sensible defaults. Tests override
// individual fields to exercise error paths.
func makeReq(t *testing.T) (*memFileStore, *stubFileService, StageRequest) {
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
	req := StageRequest{
		SessionID:       "test-session",
		ImageURL:        "minio://bucket/c1.png",
		KBBucket:        "kb-A",
		CacheKey:        "c1",
		TenantID:        7,
		URLHint:         "minio://bucket/c1.png",
		FileSink:        store,
		FileService:     fileSvc,
		StorageResolver: resolver,
		TenantService:   tenants,
	}
	return store, fileSvc, req
}

func TestStageImageBytes_FreshWrite_Success(t *testing.T) {
	store, fileSvc, req := makeReq(t)
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 1024)...)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	res, err := StageImageBytes(withSession(context.Background(), "test-session"), req)
	if err != nil {
		t.Fatalf("StageImageBytes failed: %v", err)
	}
	if res.Cached {
		t.Errorf("Cached = true on fresh write, want false")
	}
	wantPath := "/workspace/output/images/kb-A/c1.png"
	if res.Path != wantPath {
		t.Errorf("Path = %q, want %q", res.Path, wantPath)
	}
	if res.Ext != "png" {
		t.Errorf("Ext = %q, want png", res.Ext)
	}
	if res.Size != int64(len(realBytes)) {
		t.Errorf("Size = %d, want %d", res.Size, len(realBytes))
	}
	if !store.has("test-session", wantPath) {
		t.Errorf("expected bytes at %s", wantPath)
	}
	if got := atomic.LoadInt32(&fileSvc.getFileHits); got != 1 {
		t.Errorf("GetFile hits = %d, want 1", got)
	}
}

func TestStageImageBytes_IdempotentSecondCall_Cached(t *testing.T) {
	_, fileSvc, req := makeReq(t)
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 256)...)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	ctx := withSession(context.Background(), "test-session")
	first, err := StageImageBytes(ctx, req)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if first.Cached {
		t.Errorf("first call Cached = true, want false")
	}

	second, err := StageImageBytes(ctx, req)
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if !second.Cached {
		t.Errorf("second call Cached = false, want true")
	}
	if second.Path != first.Path {
		t.Errorf("Path mismatch: first=%q second=%q", first.Path, second.Path)
	}
	if got := atomic.LoadInt32(&fileSvc.getFileHits); got != 1 {
		t.Errorf("GetFile hits = %d, want 1 (second call must not re-download)", got)
	}
}

func TestStageImageBytes_UrlHintCacheHit_BeforeSniff(t *testing.T) {
	store, fileSvc, req := makeReq(t)
	// Pre-stage a file at the URL-hint extension so the hint probe hits.
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 256)...)
	store.files["test-session:/workspace/output/images/kb-A/c1.png"] = realBytes
	// GetFile should never be called when the hint probe hits.
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		t.Errorf("GetFile called on hint-cache hit")
		return nil, errors.New("unexpected")
	}

	res, err := StageImageBytes(withSession(context.Background(), "test-session"), req)
	if err != nil {
		t.Fatalf("StageImageBytes failed: %v", err)
	}
	if !res.Cached {
		t.Errorf("Cached = false, want true (URL hint probe should hit)")
	}
	if res.Path != "/workspace/output/images/kb-A/c1.png" {
		t.Errorf("Path = %q, want hint path", res.Path)
	}
	if res.Ext != "png" {
		t.Errorf("Ext = %q, want png", res.Ext)
	}
}

func TestStageImageBytes_Oversize_Refused(t *testing.T) {
	_, fileSvc, req := makeReq(t)
	huge := bytes.Repeat([]byte{0x00}, MaxBytes+1)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(huge)), nil
	}

	_, err := StageImageBytes(withSession(context.Background(), "test-session"), req)
	if err == nil {
		t.Fatal("expected oversize error")
	}
	if !contains(err.Error(), "over the") || !contains(err.Error(), "byte cap") {
		t.Errorf("Error = %q, want oversize cap message", err.Error())
	}
}

func TestStageImageBytes_MagicSniff_FallbackURL(t *testing.T) {
	_, fileSvc, req := makeReq(t)
	req.ImageURL = "minio://bucket/c1.tiff"
	req.URLHint = "minio://bucket/c1.tiff"
	noise := bytes.Repeat([]byte{0xAB}, 256)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(noise)), nil
	}

	res, err := StageImageBytes(withSession(context.Background(), "test-session"), req)
	if err != nil {
		t.Fatalf("StageImageBytes failed: %v", err)
	}
	if res.Ext != "tiff" {
		t.Errorf("Ext = %q, want tiff (URL fallback)", res.Ext)
	}
	if res.Path != "/workspace/output/images/kb-A/c1.tiff" {
		t.Errorf("Path = %q, want .tiff path", res.Path)
	}
}

func TestStageImageBytes_MagicSniff_NoFallback_Refused(t *testing.T) {
	_, fileSvc, req := makeReq(t)
	req.ImageURL = "minio://bucket/c1"
	req.URLHint = "minio://bucket/c1"
	noise := bytes.Repeat([]byte{0xAB}, 256)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(noise)), nil
	}

	_, err := StageImageBytes(withSession(context.Background(), "test-session"), req)
	if err == nil {
		t.Fatal("expected unrecognized format error")
	}
	if !contains(err.Error(), "unrecognized image format") {
		t.Errorf("Error = %q, want unrecognized format message", err.Error())
	}
}

func TestStageImageBytes_ResolverFailure_FallbackDefault(t *testing.T) {
	store, fileSvc, req := makeReq(t)
	req.StorageResolver = &stubResolver{
		resolve: func(ctx context.Context, tnt *types.Tenant, backendID, provider, baseDir string) (
			interfaces.FileService, string, error) {
			return nil, "", errors.New("resolver boom")
		},
	}
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 256)...)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	res, err := StageImageBytes(withSession(context.Background(), "test-session"), req)
	if err != nil {
		t.Fatalf("StageImageBytes failed: %v", err)
	}
	if !store.has("test-session", "/workspace/output/images/kb-A/c1.png") {
		t.Error("expected bytes via fallback file service")
	}
	if res.Path != "/workspace/output/images/kb-A/c1.png" {
		t.Errorf("Path = %q, want fallback path", res.Path)
	}
}

func TestStageImageBytes_TenantLookupFailure_FallbackDefault(t *testing.T) {
	store, fileSvc, req := makeReq(t)
	req.TenantService = &stubTenantService{
		getByID: func(ctx context.Context, id uint64) (*types.Tenant, error) {
			return nil, errors.New("tenant not found")
		},
	}
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 256)...)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	res, err := StageImageBytes(withSession(context.Background(), "test-session"), req)
	if err != nil {
		t.Fatalf("StageImageBytes failed: %v", err)
	}
	if !store.has("test-session", "/workspace/output/images/kb-A/c1.png") {
		t.Error("expected bytes via fallback file service")
	}
	if res.Path != "/workspace/output/images/kb-A/c1.png" {
		t.Errorf("Path = %q, want fallback path", res.Path)
	}
}

func TestStageImageBytes_JpegSniff(t *testing.T) {
	store, fileSvc, req := makeReq(t)
	realBytes := append(jpegMagic(), bytes.Repeat([]byte{0x00}, 256)...)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	res, err := StageImageBytes(withSession(context.Background(), "test-session"), req)
	if err != nil {
		t.Fatalf("StageImageBytes failed: %v", err)
	}
	if res.Ext != "jpg" {
		t.Errorf("Ext = %q, want jpg", res.Ext)
	}
	if !store.has("test-session", "/workspace/output/images/kb-A/c1.jpg") {
		t.Errorf("expected .jpg at sniffed path")
	}
}

func TestStageImageBytes_MissingSessionID_Refused(t *testing.T) {
	_, _, req := makeReq(t)
	req.SessionID = ""

	_, err := StageImageBytes(context.Background(), req)
	if err == nil {
		t.Fatal("expected missing-SessionID error")
	}
	if !contains(err.Error(), "SessionID is required") {
		t.Errorf("Error = %q, want SessionID required", err.Error())
	}
}

func TestStageImageBytes_MissingImageURL_Refused(t *testing.T) {
	_, _, req := makeReq(t)
	req.ImageURL = ""

	_, err := StageImageBytes(context.Background(), req)
	if err == nil {
		t.Fatal("expected missing-ImageURL error")
	}
	if !contains(err.Error(), "ImageURL is required") {
		t.Errorf("Error = %q, want ImageURL required", err.Error())
	}
}

func TestStageImageBytes_MissingCacheKey_Refused(t *testing.T) {
	_, _, req := makeReq(t)
	req.CacheKey = ""

	_, err := StageImageBytes(context.Background(), req)
	if err == nil {
		t.Fatal("expected missing-CacheKey error")
	}
	if !contains(err.Error(), "CacheKey is required") {
		t.Errorf("Error = %q, want CacheKey required", err.Error())
	}
}

func TestStageImageBytes_MissingFileSink_Refused(t *testing.T) {
	_, _, req := makeReq(t)
	req.FileSink = nil

	_, err := StageImageBytes(context.Background(), req)
	if err == nil {
		t.Fatal("expected missing-FileSink error")
	}
	if !contains(err.Error(), "FileSink is required") {
		t.Errorf("Error = %q, want FileSink required", err.Error())
	}
}

func TestStageImageBytes_GetFileFailure(t *testing.T) {
	_, fileSvc, req := makeReq(t)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return nil, errors.New("backend down")
	}

	_, err := StageImageBytes(context.Background(), req)
	if err == nil {
		t.Fatal("expected GetFile error")
	}
	if !contains(err.Error(), "failed to read image bytes") {
		t.Errorf("Error = %q, want read failure message", err.Error())
	}
}

// contains is a tiny stdlib-free substring check.
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

// TestStageImageBytes_WritesSidecar_OnFresh verifies that a successful
// fresh write produces both the image and the .txt sidecar at the
// conventional sibling path. DescriptionPath is populated accordingly.
func TestStageImageBytes_WritesSidecar_OnFresh(t *testing.T) {
	store, fileSvc, req := makeReq(t)
	req.Description = "source: kb\nkb_id: kb-A\n"
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 256)...)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	res, err := StageImageBytes(withSession(context.Background(), "test-session"), req)
	if err != nil {
		t.Fatalf("StageImageBytes failed: %v", err)
	}
	wantDescPath := "/workspace/output/images/kb-A/c1.txt"
	if res.DescriptionPath != wantDescPath {
		t.Errorf("DescriptionPath = %q, want %q", res.DescriptionPath, wantDescPath)
	}
	if !store.has("test-session", wantDescPath) {
		t.Errorf("expected sidecar at %s", wantDescPath)
	}
	body, _ := store.ReadSessionFile(context.Background(), "test-session", wantDescPath)
	if string(body) != "source: kb\nkb_id: kb-A\n" {
		t.Errorf("sidecar content = %q, want exact match", body)
	}
}

// TestStageImageBytes_ReusesSidecar_OnCacheHit verifies that the second
// call (cache hit) reports the existing sidecar path without re-writing
// it. The sidecar is written exactly once per (session, cacheKey).
func TestStageImageBytes_ReusesSidecar_OnCacheHit(t *testing.T) {
	store, fileSvc, req := makeReq(t)
	req.Description = "first sidecar"
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 256)...)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	ctx := withSession(context.Background(), "test-session")
	if _, err := StageImageBytes(ctx, req); err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	// Second call: clear Description so writeSidecar would otherwise return
	// "" — only probeExistingSidecar should report the existing path.
	req2 := req
	req2.Description = ""
	if _, err := StageImageBytes(ctx, req2); err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	body, _ := store.ReadSessionFile(context.Background(), "test-session",
		"/workspace/output/images/kb-A/c1.txt")
	if string(body) != "first sidecar" {
		t.Errorf("sidecar content = %q, want unchanged from first call", body)
	}
}

// TestStageImageBytes_DescriptionFn_LazyAndErrorTolerant verifies that
// (a) DescriptionFn is not called on cache hit (lazy), and (b) when it
// errors on a fresh write, the image stage still succeeds and the
// sidecar path stays empty.
func TestStageImageBytes_DescriptionFn_LazyAndErrorTolerant(t *testing.T) {
	store, fileSvc, req := makeReq(t)
	var descCalls int32
	req.DescriptionFn = func() (string, error) {
		atomic.AddInt32(&descCalls, 1)
		return "", errors.New("description-fn boom")
	}
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 256)...)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	res, err := StageImageBytes(withSession(context.Background(), "test-session"), req)
	if err != nil {
		t.Fatalf("image stage must succeed even when DescriptionFn errors: %v", err)
	}
	if res.DescriptionPath != "" {
		t.Errorf("DescriptionPath = %q on DescriptionFn error, want empty", res.DescriptionPath)
	}
	if got := atomic.LoadInt32(&descCalls); got != 1 {
		t.Errorf("DescriptionFn calls = %d on fresh write, want 1", got)
	}
	if !store.has("test-session", res.Path) {
		t.Errorf("expected image bytes at %s", res.Path)
	}

	// Second call (cache hit): DescriptionFn must NOT be called.
	if _, err := StageImageBytes(withSession(context.Background(), "test-session"), req); err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if got := atomic.LoadInt32(&descCalls); got != 1 {
		t.Errorf("DescriptionFn called on cache hit (calls=%d), want unchanged", got)
	}
}

// TestStageImageBytes_NoDescription_NoSidecar verifies that the default
// StageRequest (no Description / DescriptionFn) does not produce a
// sidecar — preserves backwards compatibility with callers that have
// not opted into the sidecar feature.
func TestStageImageBytes_NoDescription_NoSidecar(t *testing.T) {
	store, fileSvc, req := makeReq(t)
	realBytes := append(pngMagic(), bytes.Repeat([]byte{0x00}, 256)...)
	fileSvc.getFile = func(ctx context.Context, p string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(realBytes)), nil
	}

	res, err := StageImageBytes(withSession(context.Background(), "test-session"), req)
	if err != nil {
		t.Fatalf("StageImageBytes failed: %v", err)
	}
	if res.DescriptionPath != "" {
		t.Errorf("DescriptionPath = %q on no-description, want empty", res.DescriptionPath)
	}
	if store.has("test-session", "/workspace/output/images/kb-A/c1.txt") {
		t.Error("sidecar must not exist when Description / DescriptionFn are unset")
	}
}
