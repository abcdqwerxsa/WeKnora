package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRedisTestClient(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// interfaces_workflowSvc is the narrow slice the bridge tests need.
type interfaces_workflowSvc = interface {
	RunWorkflow(ctx context.Context, id string, req *types.RunWorkflowRequest) (*types.WorkflowRun, error)
	SubscribeWorkflowRunEvents(runID string) (<-chan types.WorkflowRunEvent, func())
}

// TestRedisBridge_CrossInstanceDelivery: publisher and subscriber live on
// DIFFERENT service instances (separate brokers) sharing one redis — the
// only delivery path is the pubsub channel. Frames are driven directly
// (deterministic; the full-run path is covered by the run tests).
func TestRedisBridge_CrossInstanceDelivery(t *testing.T) {
	client := newRedisTestClient(t)
	instanceA := NewWorkflowService(newRunRepoStub(nil), nil, nil, nil, client).(*workflowService)
	instanceB := NewWorkflowService(newRunRepoStub(nil), nil, nil, nil, client).(*workflowService)
	ctx := context.Background()
	const runID = "run-x-inst"

	ch, stop := instanceB.SubscribeWorkflowRunEvents(runID)
	defer stop()
	got := make(chan []types.WorkflowRunEvent, 1)
	go func() {
		var frames []types.WorkflowRunEvent
		for f := range ch {
			frames = append(frames, f)
		}
		got <- frames
	}()

	instanceA.publishFrameRedis(ctx, types.WorkflowRunEvent{RunID: runID, Kind: "node", NodeID: "start", Phase: "started"})
	instanceA.publishFrameRedis(ctx, types.WorkflowRunEvent{RunID: runID, Kind: "node", NodeID: "llm", Phase: "finished", DurationMS: 12})
	// Terminal frame closes B's channel even though A's broker never saw B.
	instanceA.emitRunFinished(ctx, &types.WorkflowRun{ID: runID, WorkflowID: "wf", TenantID: 7}, types.WorkflowRunStatusSucceeded, "")

	select {
	case frames := <-got:
		require.Len(t, frames, 3)
		assert.Equal(t, "start", frames[0].NodeID)
		assert.Equal(t, "llm", frames[1].NodeID)
		assert.Equal(t, "run", frames[2].Kind)
		assert.Equal(t, types.WorkflowRunStatusSucceeded, frames[2].Status)
	case <-time.After(5 * time.Second):
		t.Fatal("instance B never received frames via redis")
	}
}

// TestRedisBridge_SingleInstanceDedup: with redis configured, a run on the
// SAME instance delivers each frame exactly once (local + redis echo are
// deduplicated by kind|node|phase|duration).
func TestRedisBridge_SingleInstanceDedup(t *testing.T) {
	wf := &types.Workflow{ID: "wf-d", TenantID: 8, Name: "wf", DSL: types.JSON(linearDSL)}
	client := newRedisTestClient(t)
	svc := NewWorkflowService(newRunRepoStub(wf), &wfStubModelSvc{reply: "ok"}, nil, nil, client)
	concrete := svc.(*workflowService) // same package: reach the transport hooks
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(8))

	run, err := svc.RunWorkflow(ctx, "wf-d", &types.RunWorkflowRequest{Query: "hi"})
	require.NoError(t, err)

	ch, stop := svc.SubscribeWorkflowRunEvents(run.ID)
	defer stop()
	// Terminal replay is not the dedup path under test — feed a synthetic
	// duplicate burst directly through both transports of the SAME frame.
	frame := types.WorkflowRunEvent{RunID: run.ID, Kind: "node", NodeID: "start", Phase: "started"}
	concrete.publishFrameRedis(ctx, frame)
	concrete.runs.publish(frame) // same key: must be dropped by dedup
	concrete.publishFrameRedis(ctx, frame)
	concrete.runs.publish(frame)

	var seen int
	deadline := time.After(3 * time.Second)
	for seen == 0 {
		select {
		case f, ok := <-ch:
			if !ok {
				t.Fatal("channel closed before first frame")
			}
			assert.Equal(t, "start", f.NodeID)
			seen++
		case <-deadline:
			t.Fatal("no frame delivered")
		}
	}
	// Give duplicate paths a chance to (wrongly) deliver, then stop.
	time.Sleep(100 * time.Millisecond)
	select {
	case f := <-ch:
		t.Fatalf("duplicate frame delivered: %+v", f)
	default:
	}
	stop()
}
