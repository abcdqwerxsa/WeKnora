package service

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCheckpointTestKV(t *testing.T) (*redisCheckpointKV, *miniredis.Miniredis) {
	t.Helper()
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return newRedisCheckpointKV(client), mini
}

func TestRedisCheckpointKV_RoundtripAndPrefix(t *testing.T) {
	kv, mini := newCheckpointTestKV(t)
	ctx := context.Background()

	// Miss on unknown key: (nil, false, nil) — the "no checkpoint" contract.
	v, ok, err := kv.Get(ctx, "run-1")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, v)

	require.NoError(t, kv.Set(ctx, "run-1", []byte("checkpoint-bytes"), time.Hour))

	// Stored under the wf:ckpt: namespace prefix.
	require.True(t, mini.Exists(redisCheckpointKeyPrefix+"run-1"), "key must be namespaced")

	got, ok, err := kv.Get(ctx, "run-1")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, []byte("checkpoint-bytes"), got)

	require.NoError(t, kv.Delete(ctx, "run-1"))
	_, ok, err = kv.Get(ctx, "run-1")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRedisCheckpointKV_TTLApplied(t *testing.T) {
	kv, mini := newCheckpointTestKV(t)
	ctx := context.Background()

	require.NoError(t, kv.Set(ctx, "run-2", []byte("x"), 2*time.Hour))
	ttl := mini.TTL(redisCheckpointKeyPrefix + "run-2")
	assert.InDelta(t, 2*time.Hour, ttl, float64(time.Minute), "SET must carry PX expiry")

	// Zero TTL stores without expiry (engine contract: zero = no expiry).
	require.NoError(t, kv.Set(ctx, "run-3", []byte("x"), 0))
	assert.Equal(t, time.Duration(0), mini.TTL(redisCheckpointKeyPrefix+"run-3"))
}
