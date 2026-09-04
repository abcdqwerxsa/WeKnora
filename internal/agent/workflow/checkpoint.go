package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// KVStore is the minimal persistence surface the engine needs for
// checkpoints. The integration layer supplies a Redis-backed
// implementation; tests use MemKV. Signatures intentionally match eino's
// CheckPointStore Get/Set plus a TTL-aware Set and Delete.
type KVStore interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// KVCheckPointStore adapts a KVStore to eino's compose.CheckPointStore
// (and CheckPointDeleter). eino serializes checkpoints itself; this adapter
// stores the opaque bytes under the given key unchanged, mirroring
// RAGFlow's redis checkpoint store ("agent:cp:{id}" style keys stay the
// caller's concern — eino passes the checkpoint id through).
type KVCheckPointStore struct {
	KV  KVStore
	TTL time.Duration // applied on every Set; zero disables expiry
}

// compile-time interface checks against eino's aliases.
var (
	_ interface {
		Get(ctx context.Context, checkPointID string) ([]byte, bool, error)
		Set(ctx context.Context, checkPointID string, checkPoint []byte) error
	} = (*KVCheckPointStore)(nil)
	_ interface {
		Delete(ctx context.Context, checkPointID string) error
	} = (*KVCheckPointStore)(nil)
)

// Get loads a checkpoint. exists=false means "no checkpoint for this id".
func (s *KVCheckPointStore) Get(ctx context.Context, checkPointID string) ([]byte, bool, error) {
	if s == nil || s.KV == nil {
		return nil, false, fmt.Errorf("workflow: checkpoint store has no KV backend")
	}
	return s.KV.Get(ctx, checkPointID)
}

// Set persists a checkpoint (eino-marshaled bytes) with the store's TTL.
func (s *KVCheckPointStore) Set(ctx context.Context, checkPointID string, checkPoint []byte) error {
	if s == nil || s.KV == nil {
		return fmt.Errorf("workflow: checkpoint store has no KV backend")
	}
	return s.KV.Set(ctx, checkPointID, checkPoint, s.TTL)
}

// Delete removes a checkpoint (CheckPointDeleter).
func (s *KVCheckPointStore) Delete(ctx context.Context, checkPointID string) error {
	if s == nil || s.KV == nil {
		return fmt.Errorf("workflow: checkpoint store has no KV backend")
	}
	return s.KV.Delete(ctx, checkPointID)
}

// MemKV is an in-memory KVStore with TTL support (lazy expiry). Test-only.
type MemKV struct {
	mu      sync.RWMutex
	entries map[string]memEntry
	now     func() time.Time // injectable clock
}

type memEntry struct {
	value  []byte
	expiry time.Time // zero: no expiry
}

// NewMemKV builds an empty MemKV.
func NewMemKV() *MemKV {
	return &MemKV{entries: map[string]memEntry{}, now: time.Now}
}

func (m *MemKV) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[key]
	if !ok {
		return nil, false, nil
	}
	if !e.expiry.IsZero() && m.now().After(e.expiry) {
		return nil, false, nil
	}
	return e.value, true, nil
}

func (m *MemKV) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := memEntry{value: append([]byte(nil), value...)}
	if ttl > 0 {
		e.expiry = m.now().Add(ttl)
	}
	m.entries[key] = e
	return nil
}

func (m *MemKV) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, key)
	return nil
}

// ExportState serialises a state snapshot (checkpoint of the business
// state — orthogonal to eino's internal channel checkpoints, which
// KVCheckPointStore persists).
func ExportState(state *CanvasState) ([]byte, error) {
	if state == nil {
		return nil, fmt.Errorf("workflow: cannot export nil state")
	}
	return json.Marshal(state.Snapshot())
}

// ImportState rebuilds a CanvasState from ExportState bytes.
func ImportState(data []byte) (*CanvasState, error) {
	var snap StateSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("workflow: import state: %w", err)
	}
	st := NewCanvasState(nil, nil)
	st.Restore(&snap)
	return st, nil
}
