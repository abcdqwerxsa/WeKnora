package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A failed run resumes; a terminal run maps to 409 via the attached
// AppError (direct-handler invocation convention — see workflow_events_test.go).
func TestResumeWorkflowRunHandler_FailedResumesTerminalConflicts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   string
		wantCode int
	}{
		{"failed resumes", types.WorkflowRunStatusFailed, -1}, // -1: expect a 200 JSON body
		{"cancelled conflicts", types.WorkflowRunStatusCancelled, http.StatusConflict},
		{"succeeded conflicts", types.WorkflowRunStatusSucceeded, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, _ := json.Marshal(types.RunWorkflowRequest{Query: "q"})
			wf := &types.Workflow{ID: "wf-r", TenantID: 7, Name: "wf", DSL: types.JSON(wfSSELinearDSL)}
			repo := &wfEventsRepoStub{base: &wfEventsBaseRepo{saved: wf}}
			repo.runs = map[string]*types.WorkflowRun{
				"run-1": {ID: "run-1", TenantID: 7, WorkflowID: "wf-r", Status: tc.status, Input: types.JSON(input)},
			}
			svc := service.NewWorkflowService(repo, nil, nil, &wfEventsEnqueuer{}, nil)
			h := NewWorkflowHandler(svc)

			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/x", nil).WithContext(ctx)
			c.Params = gin.Params{{Key: "id", Value: "wf-r"}, {Key: "run_id", Value: "run-1"}}

			h.ResumeWorkflowRun(c)

			if tc.wantCode == -1 {
				require.Equal(t, http.StatusOK, rec.Code)
				assert.Contains(t, rec.Body.String(), "run-1")
			} else {
				require.Len(t, c.Errors, 1)
				var appErr *apperrors.AppError
				require.ErrorAs(t, c.Errors[0].Err, &appErr)
				assert.Equal(t, tc.wantCode, appErr.HTTPCode)
			}
		})
	}
}
