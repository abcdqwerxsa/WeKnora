package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubWikiPageService implements interfaces.WikiPageService by embedding
// it as a nil field (so unimplemented methods panic) and overriding only
// GetPageBySlug — the only method the tool calls.
type stubWikiPageService struct {
	interfaces.WikiPageService
	getBySlug func(ctx context.Context, kbID, slug string) (*types.WikiPage, error)
}

func (s *stubWikiPageService) GetPageBySlug(ctx context.Context, kbID, slug string) (*types.WikiPage, error) {
	return s.getBySlug(ctx, kbID, slug)
}

// stubKnowledgeService implements interfaces.KnowledgeService by
// overriding GetKnowledgeByID — the only method the tool calls.
type stubKnowledgeService struct {
	interfaces.KnowledgeService
	getByID func(ctx context.Context, id string) (*types.Knowledge, error)
}

func (s *stubKnowledgeService) GetKnowledgeByID(ctx context.Context, id string) (*types.Knowledge, error) {
	return s.getByID(ctx, id)
}

// stubWikiKnowledgeBaseService implements interfaces.KnowledgeBaseService
// for the sidecar lookup. Default fixture returns an error so the sidecar's
// kb_name field stays empty; tests that want a populated sidecar override
// getByID.
type stubWikiKnowledgeBaseService struct {
	interfaces.KnowledgeBaseService
	getByID func(ctx context.Context, id string) (*types.KnowledgeBase, error)
}

func (s *stubWikiKnowledgeBaseService) GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error) {
	return s.getByID(ctx, id)
}

// stubChunkService implements interfaces.ChunkService by overriding
// ListPagedChunksByKnowledgeID — the only method the tool calls.
type stubChunkServiceForWiki struct {
	interfaces.ChunkService
	listPaged func(ctx context.Context, knowledgeID string, page *types.Pagination,
		chunkType []types.ChunkType) (*types.PageResult, error)
}

func (s *stubChunkServiceForWiki) ListPagedChunksByKnowledgeID(
	ctx context.Context, knowledgeID string, page *types.Pagination, chunkType []types.ChunkType,
) (*types.PageResult, error) {
	return s.listPaged(ctx, knowledgeID, page, chunkType)
}

// newWikiTestTool wires a WikiGetImageTool with stub deps that satisfy
// the common success path. Tests override individual deps on the returned
// struct to exercise error paths.
func newWikiTestTool(t *testing.T) (*WikiGetImageTool, *memFileStore, *stubFileService) {
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
	pages := &stubWikiPageService{
		getBySlug: func(ctx context.Context, kbID, slug string) (*types.WikiPage, error) {
			return nil, errors.New("not implemented in default stub")
		},
	}
	knowledges := &stubKnowledgeService{
		getByID: func(ctx context.Context, id string) (*types.Knowledge, error) {
			return nil, errors.New("not implemented in default stub")
		},
	}
	kbases := &stubWikiKnowledgeBaseService{
		getByID: func(ctx context.Context, id string) (*types.KnowledgeBase, error) {
			return nil, errors.New("not implemented in default stub")
		},
	}
	chunks := &stubChunkServiceForWiki{
		listPaged: func(ctx context.Context, knowledgeID string, page *types.Pagination,
			chunkType []types.ChunkType) (*types.PageResult, error) {
			return nil, errors.New("not implemented in default stub")
		},
	}
	targets := types.SearchTargets{
		{KnowledgeBaseID: "kb-A", TenantID: 7},
	}
	wikiKBIDs := []string{"kb-A"}
	routes := NewWikiRouteResolver()
	tool := NewWikiGetImageTool(pages, knowledges, kbases, chunks, fileSvc, resolver, tenants,
		targets, wikiKBIDs, routes, store)
	return tool, store, fileSvc
}

// makeWikiPage builds a *types.WikiPage with sane defaults for tests.
func makeWikiPage(slug string, sourceRefs []string) *types.WikiPage {
	return &types.WikiPage{
		Slug:            slug,
		SourceRefs:      sourceRefs,
		Content:         "stub",
		KnowledgeBaseID: "kb-A",
	}
}

// makeImageChunk builds an image_ocr chunk with sensible defaults.
func makeImageChunk(id, kbID, knowledgeID, url string) *types.Chunk {
	infos, _ := json.Marshal([]types.ImageInfo{{URL: url, Caption: "caption for " + id}})
	return &types.Chunk{
		ID:              id,
		KnowledgeBaseID: kbID,
		KnowledgeID:     knowledgeID,
		TenantID:        7,
		ChunkType:       types.ChunkTypeImageOCR,
		ChunkIndex:      0,
		ImageInfo:       string(infos),
	}
}

// makeKnowledge builds a *types.Knowledge with sensible defaults.
func makeKnowledge(id, kbID string) *types.Knowledge {
	return &types.Knowledge{
		ID:              id,
		KnowledgeBaseID: kbID,
		TenantID:        7,
		Title:           "Test Doc",
	}
}

func TestWikiGetImage_HappyPath_SingleImage(t *testing.T) {
	tool, store, _ := newWikiTestTool(t)
	tool.wikiPageService.(*stubWikiPageService).getBySlug = func(
		ctx context.Context, kbID, slug string,
	) (*types.WikiPage, error) {
		return makeWikiPage(slug, []string{"doc-1"}), nil
	}
	tool.knowledgeService.(*stubKnowledgeService).getByID = func(
		ctx context.Context, id string,
	) (*types.Knowledge, error) {
		return makeKnowledge(id, "kb-A"), nil
	}
	tool.chunkService.(*stubChunkServiceForWiki).listPaged = func(
		ctx context.Context, knowledgeID string, page *types.Pagination,
		chunkType []types.ChunkType,
	) (*types.PageResult, error) {
		return &types.PageResult{
			Total:    1,
			Page:     1,
			PageSize: 100,
			Data:     []*types.Chunk{makeImageChunk("c1", "kb-A", "doc-1", "resource://abc")},
		}, nil
	}

	res, err := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"slug":"summary/abc","image_index":0}`))
	if err != nil {
		t.Fatalf("Execute returned err: %v", err)
	}
	if !res.Success {
		t.Fatalf("Execute failed: %s", res.Error)
	}
	wantPath := "/workspace/output/images/wiki/summary_abc_idx0.png"
	if got := res.Data["path"]; got != wantPath {
		t.Errorf("path = %v, want %s", got, wantPath)
	}
	if got := res.Data["mime"]; got != "png" {
		t.Errorf("mime = %v, want png", got)
	}
	if got := res.Data["slug"]; got != "summary/abc" {
		t.Errorf("slug = %v, want summary/abc", got)
	}
	if got := res.Data["image_index"]; got != 0 {
		t.Errorf("image_index = %v, want 0", got)
	}
	if got := res.Data["kb_id"]; got != "kb-A" {
		t.Errorf("kb_id = %v, want kb-A", got)
	}
	if got := res.Data["knowledge_id"]; got != "doc-1" {
		t.Errorf("knowledge_id = %v, want doc-1", got)
	}
	if got := res.Data["cached"]; got != false {
		t.Errorf("cached = %v, want false", got)
	}
	if !store.has("test-session", wantPath) {
		t.Errorf("expected bytes at %s", wantPath)
	}
	// Candidate list must contain exactly one entry.
	imgs, ok := res.Data["images"].([]map[string]interface{})
	if !ok {
		t.Fatalf("images field missing or wrong type: %T", res.Data["images"])
	}
	if len(imgs) != 1 {
		t.Errorf("images len = %d, want 1", len(imgs))
	}
	if imgs[0]["chunk_id"] != "c1" {
		t.Errorf("images[0].chunk_id = %v, want c1", imgs[0]["chunk_id"])
	}
}

func TestWikiGetImage_HappyPath_MultiImage_IndexPick(t *testing.T) {
	tool, store, _ := newWikiTestTool(t)
	tool.wikiPageService.(*stubWikiPageService).getBySlug = func(
		ctx context.Context, kbID, slug string,
	) (*types.WikiPage, error) {
		return makeWikiPage(slug, []string{"doc-1"}), nil
	}
	tool.knowledgeService.(*stubKnowledgeService).getByID = func(
		ctx context.Context, id string,
	) (*types.Knowledge, error) {
		return makeKnowledge(id, "kb-A"), nil
	}
	tool.chunkService.(*stubChunkServiceForWiki).listPaged = func(
		ctx context.Context, knowledgeID string, page *types.Pagination,
		chunkType []types.ChunkType,
	) (*types.PageResult, error) {
		c1 := makeImageChunk("c1", "kb-A", "doc-1", "resource://a")
		c1.ChunkIndex = 1
		c2 := makeImageChunk("c2", "kb-A", "doc-1", "resource://b")
		c2.ChunkIndex = 2
		c3 := makeImageChunk("c3", "kb-A", "doc-1", "resource://c")
		c3.ChunkIndex = 3
		return &types.PageResult{
			Total:    3,
			Page:     1,
			PageSize: 100,
			Data:     []*types.Chunk{c1, c2, c3},
		}, nil
	}

	res, err := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"slug":"summary/abc","image_index":2}`))
	if err != nil || !res.Success {
		t.Fatalf("Execute failed: %v %s", err, res.Error)
	}
	wantPath := "/workspace/output/images/wiki/summary_abc_idx2.png"
	if got := res.Data["path"]; got != wantPath {
		t.Errorf("path = %v, want %s", got, wantPath)
	}
	if got := res.Data["image_index"]; got != 2 {
		t.Errorf("image_index = %v, want 2", got)
	}
	if !store.has("test-session", wantPath) {
		t.Errorf("expected bytes at %s", wantPath)
	}
	imgs := res.Data["images"].([]map[string]interface{})
	if len(imgs) != 3 {
		t.Fatalf("images len = %d, want 3", len(imgs))
	}
	// Verify each candidate has the right index + chunk_id.
	wantChunks := []string{"c1", "c2", "c3"}
	for i, want := range wantChunks {
		if imgs[i]["index"] != i {
			t.Errorf("imgs[%d].index = %v, want %d", i, imgs[i]["index"], i)
		}
		if imgs[i]["chunk_id"] != want {
			t.Errorf("imgs[%d].chunk_id = %v, want %s", i, imgs[i]["chunk_id"], want)
		}
	}
}

func TestWikiGetImage_PageNotFound(t *testing.T) {
	tool, _, _ := newWikiTestTool(t)
	tool.wikiPageService.(*stubWikiPageService).getBySlug = func(
		ctx context.Context, kbID, slug string,
	) (*types.WikiPage, error) {
		return nil, errors.New("page not found")
	}

	res, _ := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"slug":"summary/does-not-exist"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected page-not-found error")
	}
	if !contains(res.Error, "wiki page not found") {
		t.Errorf("Error = %q, want wiki page not found", res.Error)
	}
}

func TestWikiGetImage_NoSourceRefs(t *testing.T) {
	tool, _, _ := newWikiTestTool(t)
	tool.wikiPageService.(*stubWikiPageService).getBySlug = func(
		ctx context.Context, kbID, slug string,
	) (*types.WikiPage, error) {
		return makeWikiPage(slug, nil), nil // empty SourceRefs
	}

	res, _ := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"slug":"summary/no-sources"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected no-source-refs error")
	}
	if !contains(res.Error, "no source documents") {
		t.Errorf("Error = %q, want 'no source documents'", res.Error)
	}
}

func TestWikiGetImage_NoImageChunks(t *testing.T) {
	tool, _, _ := newWikiTestTool(t)
	tool.wikiPageService.(*stubWikiPageService).getBySlug = func(
		ctx context.Context, kbID, slug string,
	) (*types.WikiPage, error) {
		return makeWikiPage(slug, []string{"doc-1"}), nil
	}
	tool.knowledgeService.(*stubKnowledgeService).getByID = func(
		ctx context.Context, id string,
	) (*types.Knowledge, error) {
		return makeKnowledge(id, "kb-A"), nil
	}
	tool.chunkService.(*stubChunkServiceForWiki).listPaged = func(
		ctx context.Context, knowledgeID string, page *types.Pagination,
		chunkType []types.ChunkType,
	) (*types.PageResult, error) {
		return &types.PageResult{
			Total:    0,
			Page:     1,
			PageSize: 100,
			Data:     []*types.Chunk{},
		}, nil
	}

	res, _ := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"slug":"summary/no-images"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected no-images error")
	}
	if !contains(res.Error, "no image chunks") {
		t.Errorf("Error = %q, want 'no image chunks'", res.Error)
	}
}

func TestWikiGetImage_ImageIndexOutOfRange(t *testing.T) {
	tool, _, _ := newWikiTestTool(t)
	tool.wikiPageService.(*stubWikiPageService).getBySlug = func(
		ctx context.Context, kbID, slug string,
	) (*types.WikiPage, error) {
		return makeWikiPage(slug, []string{"doc-1"}), nil
	}
	tool.knowledgeService.(*stubKnowledgeService).getByID = func(
		ctx context.Context, id string,
	) (*types.Knowledge, error) {
		return makeKnowledge(id, "kb-A"), nil
	}
	tool.chunkService.(*stubChunkServiceForWiki).listPaged = func(
		ctx context.Context, knowledgeID string, page *types.Pagination,
		chunkType []types.ChunkType,
	) (*types.PageResult, error) {
		return &types.PageResult{
			Total:    2,
			Page:     1,
			PageSize: 100,
			Data: []*types.Chunk{
				makeImageChunk("c1", "kb-A", "doc-1", "resource://a"),
				makeImageChunk("c2", "kb-A", "doc-1", "resource://b"),
			},
		}, nil
	}

	res, _ := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"slug":"summary/abc","image_index":5}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected out-of-range error")
	}
	if !contains(res.Error, "image_index out of range") {
		t.Errorf("Error = %q, want out-of-range message", res.Error)
	}
	if !contains(res.Error, "2") {
		t.Errorf("Error = %q, want mention of valid index range", res.Error)
	}
}

func TestWikiGetImage_NegativeImageIndex(t *testing.T) {
	tool, _, _ := newWikiTestTool(t)
	res, _ := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"slug":"summary/abc","image_index":-1}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected negative-index error")
	}
	if !contains(res.Error, "image_index must be") {
		t.Errorf("Error = %q, want 'image_index must be' message", res.Error)
	}
}

func TestWikiGetImage_KBScopeViolation(t *testing.T) {
	tool, store, _ := newWikiTestTool(t)
	tool.wikiPageService.(*stubWikiPageService).getBySlug = func(
		ctx context.Context, kbID, slug string,
	) (*types.WikiPage, error) {
		return makeWikiPage(slug, []string{"doc-1"}), nil
	}
	// Source belongs to a KB NOT in agent's searchTargets.
	tool.knowledgeService.(*stubKnowledgeService).getByID = func(
		ctx context.Context, id string,
	) (*types.Knowledge, error) {
		return makeKnowledge(id, "kb-OUT-OF-SCOPE"), nil
	}

	res, _ := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"slug":"summary/abc"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected scope refusal")
	}
	if !contains(res.Error, "wiki page not found") {
		t.Errorf("Error = %q, want generic not-found (no leak)", res.Error)
	}
	if len(store.files) != 0 {
		t.Errorf("sandbox must remain empty after refusal; has %d files", len(store.files))
	}
}

func TestWikiGetImage_EmptySlug_Refused(t *testing.T) {
	tool, _, _ := newWikiTestTool(t)
	res, _ := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"slug":""}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected missing-slug refusal")
	}
	if !contains(res.Error, "slug is required") {
		t.Errorf("Error = %q, want 'slug is required'", res.Error)
	}
}

func TestWikiGetImage_MissingSessionID_Refused(t *testing.T) {
	tool, _, _ := newWikiTestTool(t)
	res, _ := tool.Execute(context.Background(),
		json.RawMessage(`{"slug":"summary/abc"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected missing-session refusal")
	}
	if !contains(res.Error, "no session ID") {
		t.Errorf("Error = %q, want missing-session message", res.Error)
	}
}

func TestWikiGetImage_SourceKnowledgeNotFound(t *testing.T) {
	tool, store, _ := newWikiTestTool(t)
	tool.wikiPageService.(*stubWikiPageService).getBySlug = func(
		ctx context.Context, kbID, slug string,
	) (*types.WikiPage, error) {
		return makeWikiPage(slug, []string{"doc-1"}), nil
	}
	tool.knowledgeService.(*stubKnowledgeService).getByID = func(
		ctx context.Context, id string,
	) (*types.Knowledge, error) {
		return nil, errors.New("knowledge not found")
	}

	res, _ := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"slug":"summary/abc"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected source-not-found error")
	}
	if !contains(res.Error, "source document not found") {
		t.Errorf("Error = %q, want source-not-found message", res.Error)
	}
	if len(store.files) != 0 {
		t.Errorf("sandbox must remain empty after refusal; has %d files", len(store.files))
	}
}

func TestWikiGetImage_EntitySlug_NotSummary(t *testing.T) {
	// The kb_get_image trap rejects summary/<uuid> but accepts other
	// slugs only by accident. wiki_get_image must accept non-summary
	// slugs like entity/foo uniformly — no special-casing.
	tool, store, _ := newWikiTestTool(t)
	tool.wikiPageService.(*stubWikiPageService).getBySlug = func(
		ctx context.Context, kbID, slug string,
	) (*types.WikiPage, error) {
		return makeWikiPage(slug, []string{"doc-1"}), nil
	}
	tool.knowledgeService.(*stubKnowledgeService).getByID = func(
		ctx context.Context, id string,
	) (*types.Knowledge, error) {
		return makeKnowledge(id, "kb-A"), nil
	}
	tool.chunkService.(*stubChunkServiceForWiki).listPaged = func(
		ctx context.Context, knowledgeID string, page *types.Pagination,
		chunkType []types.ChunkType,
	) (*types.PageResult, error) {
		return &types.PageResult{
			Total:    1,
			Page:     1,
			PageSize: 100,
			Data:     []*types.Chunk{makeImageChunk("c1", "kb-A", "doc-1", "resource://a")},
		}, nil
	}

	res, _ := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"slug":"entity/acme-corp"}`))
	if !res.Success {
		t.Fatalf("Execute failed for entity/ slug: %s", res.Error)
	}
	wantPath := "/workspace/output/images/wiki/entity_acme-corp_idx0.png"
	if got := res.Data["path"]; got != wantPath {
		t.Errorf("path = %v, want %s", got, wantPath)
	}
	if !store.has("test-session", wantPath) {
		t.Errorf("expected bytes at %s", wantPath)
	}
}

func TestWikiGetImage_FiltersChunksWithoutImageInfo(t *testing.T) {
	// image_ocr chunks with empty ImageInfo must be filtered out before
	// the candidate list is built. Otherwise an empty-ImageInfo row
	// would become "not an image chunk" at pick time and produce a
	// confusing error rather than a clean "0 images" message.
	tool, _, _ := newWikiTestTool(t)
	tool.wikiPageService.(*stubWikiPageService).getBySlug = func(
		ctx context.Context, kbID, slug string,
	) (*types.WikiPage, error) {
		return makeWikiPage(slug, []string{"doc-1"}), nil
	}
	tool.knowledgeService.(*stubKnowledgeService).getByID = func(
		ctx context.Context, id string,
	) (*types.Knowledge, error) {
		return makeKnowledge(id, "kb-A"), nil
	}
	tool.chunkService.(*stubChunkServiceForWiki).listPaged = func(
		ctx context.Context, knowledgeID string, page *types.Pagination,
		chunkType []types.ChunkType,
	) (*types.PageResult, error) {
		empty := &types.Chunk{
			ID: "c-empty", KnowledgeBaseID: "kb-A", KnowledgeID: "doc-1",
			TenantID: 7, ChunkType: types.ChunkTypeImageOCR, ChunkIndex: 1,
			ImageInfo: "", // explicitly empty
		}
		bad := &types.Chunk{
			ID: "c-bad", KnowledgeBaseID: "kb-A", KnowledgeID: "doc-1",
			TenantID: 7, ChunkType: types.ChunkTypeImageOCR, ChunkIndex: 2,
			ImageInfo: `not-json`,
		}
		return &types.PageResult{
			Total:    2,
			Page:     1,
			PageSize: 100,
			Data:     []*types.Chunk{empty, bad},
		}, nil
	}

	res, _ := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"slug":"summary/abc"}`))
	if res.Success {
		t.Fatal("Execute succeeded; expected no-image-chunks error after filtering")
	}
	if !contains(res.Error, "no image chunks") {
		t.Errorf("Error = %q, want 'no image chunks' after filter", res.Error)
	}
}

func TestWikiGetImage_Idempotency_SecondCallCached(t *testing.T) {
	tool, store, fileSvc := newWikiTestTool(t)
	tool.wikiPageService.(*stubWikiPageService).getBySlug = func(
		ctx context.Context, kbID, slug string,
	) (*types.WikiPage, error) {
		return makeWikiPage(slug, []string{"doc-1"}), nil
	}
	tool.knowledgeService.(*stubKnowledgeService).getByID = func(
		ctx context.Context, id string,
	) (*types.Knowledge, error) {
		return makeKnowledge(id, "kb-A"), nil
	}
	tool.chunkService.(*stubChunkServiceForWiki).listPaged = func(
		ctx context.Context, knowledgeID string, page *types.Pagination,
		chunkType []types.ChunkType,
	) (*types.PageResult, error) {
		return &types.PageResult{
			Total:    1,
			Page:     1,
			PageSize: 100,
			Data:     []*types.Chunk{makeImageChunk("c1", "kb-A", "doc-1", "resource://a")},
		}, nil
	}

	ctx := withSession(context.Background())
	first, _ := tool.Execute(ctx, json.RawMessage(`{"slug":"summary/abc"}`))
	if !first.Success {
		t.Fatalf("first call failed: %s", first.Error)
	}
	if first.Data["cached"] != false {
		t.Errorf("first call cached = %v, want false", first.Data["cached"])
	}
	hitsAfterFirst := atomic.LoadInt32(&fileSvc.getFileHits)

	second, _ := tool.Execute(ctx, json.RawMessage(`{"slug":"summary/abc"}`))
	if !second.Success {
		t.Fatalf("second call failed: %s", second.Error)
	}
	if second.Data["cached"] != true {
		t.Errorf("second call cached = %v, want true", second.Data["cached"])
	}
	if got := atomic.LoadInt32(&fileSvc.getFileHits); got != hitsAfterFirst {
		t.Errorf("GetFile hits = %d after second call; want unchanged", got)
	}
	if !store.has("test-session", "/workspace/output/images/wiki/summary_abc_idx0.png") {
		t.Error("expected bytes at cached path")
	}
}

// Compile-time sanity: ensure the tool satisfies the types.Tool interface
// so a missing-method regression fails the build instead of going silent.
var _ types.Tool = (*WikiGetImageTool)(nil)

// TestWikiGetImage_Sidecar_PopulatedOnSuccess asserts the wiki sidecar
// carries WikiPage (slug/title/summary), source Document title, KB name,
// chunk caption / content, and the original image URL.
func TestWikiGetImage_Sidecar_PopulatedOnSuccess(t *testing.T) {
	tool, store, _ := newWikiTestTool(t)
	tool.wikiPageService.(*stubWikiPageService).getBySlug = func(
		ctx context.Context, kbID, slug string,
	) (*types.WikiPage, error) {
		return &types.WikiPage{
			Slug:            slug,
			SourceRefs:      []string{"doc-1"},
			KnowledgeBaseID: "kb-A",
			Title:           "系统架构总览",
			Summary:         "本页综合了 2026 Q1 产品手册第 3 章。",
			Content:         "stub",
		}, nil
	}
	tool.knowledgeService.(*stubKnowledgeService).getByID = func(
		ctx context.Context, id string,
	) (*types.Knowledge, error) {
		return &types.Knowledge{
			ID:              id,
			KnowledgeBaseID: "kb-A",
			Title:           "2026 Q1 产品手册",
		}, nil
	}
	tool.knowledgeBaseService.(*stubWikiKnowledgeBaseService).getByID = func(
		ctx context.Context, id string,
	) (*types.KnowledgeBase, error) {
		return &types.KnowledgeBase{ID: id, Name: "产品手册库"}, nil
	}
	tool.chunkService.(*stubChunkServiceForWiki).listPaged = func(
		ctx context.Context, knowledgeID string, page *types.Pagination,
		chunkType []types.ChunkType,
	) (*types.PageResult, error) {
		chunk := makeImageChunk("c1", "kb-A", "doc-1", "resource://abc")
		chunk.Content = "接入层负责请求路由与限流。"
		return &types.PageResult{
			Total: 1, Page: 1, PageSize: 100,
			Data: []*types.Chunk{chunk},
		}, nil
	}

	res, err := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"slug":"summary/abc"}`))
	if err != nil || !res.Success {
		t.Fatalf("Execute failed: %v %s", err, res.Error)
	}

	wantDescPath := "/workspace/output/images/wiki/summary_abc_idx0.txt"
	if got := res.Data["description_path"]; got != wantDescPath {
		t.Errorf("description_path = %v, want %s", got, wantDescPath)
	}
	if !store.has("test-session", wantDescPath) {
		t.Fatalf("expected sidecar at %s", wantDescPath)
	}
	body, _ := store.ReadSessionFile(context.Background(), "test-session", wantDescPath)
	checks := []string{
		"source: wiki",
		"kb_id: kb-A",
		"kb_name: 产品手册库",
		"document_id: doc-1",
		"document_title: 2026 Q1 产品手册",
		"wiki_slug: summary/abc",
		"wiki_title: 系统架构总览",
		"wiki_summary: |",
		"本页综合了 2026 Q1 产品手册第 3 章。",
		"chunk_id: c1",
		"image_index: 0",
		"caption: caption for c1",
		"content: |",
		"接入层负责请求路由与限流。",
		"image_url: resource://abc",
	}
	for _, want := range checks {
		if !contains(string(body), want) {
			t.Errorf("sidecar missing %q; body=%q", want, body)
		}
	}
}

// TestWikiGetImage_Sidecar_KBLookupFailure_StillWrites verifies the wiki
// sidecar is still written when KB lookup fails (enhancement-layer
// metadata, not data-integrity-required).
func TestWikiGetImage_Sidecar_KBLookupFailure_StillWrites(t *testing.T) {
	tool, store, _ := newWikiTestTool(t)
	tool.wikiPageService.(*stubWikiPageService).getBySlug = func(
		ctx context.Context, kbID, slug string,
	) (*types.WikiPage, error) {
		return makeWikiPage(slug, []string{"doc-1"}), nil
	}
	tool.knowledgeService.(*stubKnowledgeService).getByID = func(
		ctx context.Context, id string,
	) (*types.Knowledge, error) {
		return makeKnowledge(id, "kb-A"), nil
	}
	// Default KB stub returns an error — kb_name will be absent.
	tool.chunkService.(*stubChunkServiceForWiki).listPaged = func(
		ctx context.Context, knowledgeID string, page *types.Pagination,
		chunkType []types.ChunkType,
	) (*types.PageResult, error) {
		return &types.PageResult{
			Total: 1, Page: 1, PageSize: 100,
			Data: []*types.Chunk{makeImageChunk("c1", "kb-A", "doc-1", "resource://abc")},
		}, nil
	}

	res, _ := tool.Execute(withSession(context.Background()),
		json.RawMessage(`{"slug":"summary/abc"}`))
	if !res.Success {
		t.Fatalf("Execute failed: %s", res.Error)
	}
	wantDescPath := "/workspace/output/images/wiki/summary_abc_idx0.txt"
	if !store.has("test-session", wantDescPath) {
		t.Fatalf("expected sidecar at %s even with KB lookup failure", wantDescPath)
	}
	body, _ := store.ReadSessionFile(context.Background(), "test-session", wantDescPath)
	if contains(string(body), "kb_name:") {
		t.Errorf("expected kb_name to be absent on KB lookup failure; body=%q", body)
	}
	// KB context fields that don't depend on the failing lookup are still present.
	if !contains(string(body), "wiki_slug: summary/abc") {
		t.Errorf("wiki_slug must still be present; body=%q", body)
	}
}
