package workflow

import (
	"context"
	"testing"
	"time"
)

func TestMemKVBasics(t *testing.T) {
	kv := NewMemKV()
	ctx := context.Background()

	if _, ok, err := kv.Get(ctx, "k"); err != nil || ok {
		t.Fatalf("get missing = %v %v, want false nil", ok, err)
	}
	if err := kv.Set(ctx, "k", []byte("v1"), 0); err != nil {
		t.Fatalf("set: %v", err)
	}
	if v, ok, _ := kv.Get(ctx, "k"); !ok || string(v) != "v1" {
		t.Fatalf("get = %q %v", v, ok)
	}
	if err := kv.Delete(ctx, "k"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := kv.Get(ctx, "k"); ok {
		t.Fatal("deleted key must be gone")
	}
}

func TestMemKVTTExpiry(t *testing.T) {
	kv := NewMemKV()
	base := time.Unix(1000, 0)
	now := base
	kv.now = func() time.Time { return now }

	if err := kv.Set(context.Background(), "k", []byte("v"), 10*time.Second); err != nil {
		t.Fatalf("set: %v", err)
	}
	now = base.Add(5 * time.Second)
	if _, ok, _ := kv.Get(context.Background(), "k"); !ok {
		t.Error("before TTL must hit")
	}
	now = base.Add(11 * time.Second)
	if _, ok, _ := kv.Get(context.Background(), "k"); ok {
		t.Error("after TTL must miss")
	}
}

func TestKVCheckPointStoreDelegation(t *testing.T) {
	kv := NewMemKV()
	store := &KVCheckPointStore{KV: kv, TTL: time.Minute}
	ctx := context.Background()

	if _, ok, err := store.Get(ctx, "cp1"); err != nil || ok {
		t.Fatalf("get missing = %v %v", ok, err)
	}
	if err := store.Set(ctx, "cp1", []byte("checkpoint-bytes")); err != nil {
		t.Fatalf("set: %v", err)
	}
	v, ok, err := store.Get(ctx, "cp1")
	if err != nil || !ok || string(v) != "checkpoint-bytes" {
		t.Fatalf("get = %q %v %v", v, ok, err)
	}
	if err := store.Delete(ctx, "cp1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := store.Get(ctx, "cp1"); ok {
		t.Error("deleted checkpoint must be gone")
	}
}

func TestKVCheckPointStoreWithoutBackend(t *testing.T) {
	store := &KVCheckPointStore{}
	if _, _, err := store.Get(context.Background(), "x"); err == nil {
		t.Error("nil KV backend must error, not panic")
	}
}

func TestCompileRunWithCheckpointKV(t *testing.T) {
	kv := NewMemKV()
	deps := linearDeps(&eventLog{})
	deps.CheckpointKV = kv
	deps.CheckpointTTL = time.Hour

	wf, err := Compile(linearDSL(), deps)
	if err != nil {
		t.Fatalf("Compile with checkpoint store: %v", err)
	}
	if _, err := wf.Run(context.Background(), "q", nil); err != nil {
		t.Fatalf("Run with checkpoint store: %v", err)
	}
}

func TestExportImportStateRoundtrip(t *testing.T) {
	st := NewCanvasState(map[string]any{"query": "q1", "files": []string{"a.pdf"}}, map[string]any{"model": "m"})
	st.SetOutput("llm", "content", "generated")
	st.AppendPath("start")
	st.AppendPath("llm")

	data, err := ExportState(st)
	if err != nil {
		t.Fatalf("ExportState: %v", err)
	}

	back, err := ImportState(data)
	if err != nil {
		t.Fatalf("ImportState: %v", err)
	}
	snap := back.Snapshot()
	if v, _ := snap.Outputs["llm"]["content"]; v != "generated" {
		t.Errorf("outputs roundtrip = %v", snap.Outputs)
	}
	if snap.Sys["query"] != "q1" || snap.Env["model"] != "m" {
		t.Errorf("sys/env roundtrip = %v %v", snap.Sys, snap.Env)
	}
	if len(snap.Path) != 2 {
		t.Errorf("path roundtrip = %v", snap.Path)
	}

	if _, err := ImportState([]byte("not json")); err == nil {
		t.Error("garbage import must error")
	}
	if _, err := ExportState(nil); err == nil {
		t.Error("nil export must error")
	}
}
