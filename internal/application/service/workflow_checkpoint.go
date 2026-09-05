// workflow_checkpoint.go — Redis-backed engine.KVStore for workflow
// checkpoints.
//
// Placement note: this deliberately lives in the service (integration)
// layer, NOT in internal/agent/workflow. The engine package is kept free
// of infrastructure imports (redis, logger, config) by design — see its
// package doc. engine.KVStore is a small interface, so implementing it
// here costs one adapter while preserving that boundary.

package service

import (
	"context"
	"fmt"
	"time"

	wfengine "github.com/Tencent/WeKnora/internal/agent/workflow"
	"github.com/redis/go-redis/v9"
)

// workflowCheckpointTTL bounds how long a run's checkpoints stay resumable
// after the run terminates. 24h: long enough for "resume tomorrow morning"
// operational recovery, short enough that abandoned failed runs do not
// accumulate forever (the run row outlives this and remains the record).
const workflowCheckpointTTL = 24 * time.Hour

// redisCheckpointKeyPrefix namespaces workflow checkpoint keys in redis.
// Engine keys below this prefix are: the raw eino checkpoint (key = run id)
// and the CanvasState side-car (key = run id + "#ctx", managed by
// Workflow.RunWithOptions).
const redisCheckpointKeyPrefix = "wf:ckpt:"

// redisCheckpointKV adapts *redis.Client onto wfengine.KVStore. TTL is
// applied via SET PX; a zero ttl stores without expiry.
type redisCheckpointKV struct {
	client *redis.Client
}

// compile-time check: the adapter satisfies the engine's KV contract.
var _ wfengine.KVStore = (*redisCheckpointKV)(nil)

func newRedisCheckpointKV(client *redis.Client) *redisCheckpointKV {
	return &redisCheckpointKV{client: client}
}

func (k *redisCheckpointKV) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if k == nil || k.client == nil {
		return nil, false, fmt.Errorf("workflow checkpoint kv: no redis client")
	}
	data, err := k.client.Get(ctx, redisCheckpointKeyPrefix+key).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (k *redisCheckpointKV) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if k == nil || k.client == nil {
		return fmt.Errorf("workflow checkpoint kv: no redis client")
	}
	if ttl > 0 {
		return k.client.Set(ctx, redisCheckpointKeyPrefix+key, value, ttl).Err()
	}
	return k.client.Set(ctx, redisCheckpointKeyPrefix+key, value, 0).Err()
}

func (k *redisCheckpointKV) Delete(ctx context.Context, key string) error {
	if k == nil || k.client == nil {
		return fmt.Errorf("workflow checkpoint kv: no redis client")
	}
	return k.client.Del(ctx, redisCheckpointKeyPrefix+key).Err()
}
