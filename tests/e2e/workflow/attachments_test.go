package e2e

// Run attachments end-to-end: upload → poll ready → run with files → the
// LLM answer must reference the uploaded document's content.
import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodeAttachmentID(t *testing.T, resp *http.Response) string {
	t.Helper()
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, jsonDecode(resp.Body, &out))
	require.NotEmpty(t, out.Data.ID)
	return out.Data.ID
}

func TestRunAttachmentsReachLLMContext(t *testing.T) {
	llmSkipIfNoModel(t)
	comps := linearComps(
		node("start", "Start", map[string]any{"fields": []any{}}),
		node("llm", "LLM", map[string]any{
			"model":      chatModelID,
			"prompt":     "附件文档中的暗号是什么?只用一个词回答。",
			"max_tokens": 48,
		}),
		node("ans", "Answer", map[string]any{"template": "{llm@content}"}),
	)
	id := createWorkflow(t, "run-attachments", comps)

	// Upload through the panel-facing endpoint, then poll until parsed.
	var attachmentID string
	{
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, err := mw.CreateFormFile("file", "secret-notes.md")
		require.NoError(t, err)
		fmt.Fprintf(fw, "# 会议纪要\n\n本次项目暗号: ZEBRA-7749。\n其余内容与暗号无关。\n")
		require.NoError(t, mw.Close())
		req, err := http.NewRequest("POST", baseURL+"/api/v1/workflows/"+id+"/run-attachments", &body)
		require.NoError(t, err)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := httpCli.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		attachmentID = decodeAttachmentID(t, resp)
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		r := call(t, "GET", "/api/v1/workflows/"+id+"/run-attachments/"+attachmentID, nil)
		var doc struct {
			Status       string `json:"status"`
			ChunkCount   int    `json:"chunk_count"`
			ErrorMessage string `json:"error_message"`
		}
		decodeInto(t, r.Data, &doc)
		if doc.Status == "ready" {
			break
		}
		if doc.Status == "failed" {
			t.Fatalf("attachment parse failed: %s", doc.ErrorMessage)
		}
		time.Sleep(2 * time.Second)
	}

	r := requireRunSucceeded(t, id, map[string]any{
		"query": "附件里的暗号是什么",
		"files": []string{attachmentID},
	})
	ans := answerOf(t, r)
	assert.Contains(t, ans, "ZEBRA", "answer must draw on the attached document: %s", ans)
}
