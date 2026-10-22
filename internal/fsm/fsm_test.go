package fsm

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/hashicorp/raft"
)

func apply(t *testing.T, k *KV, c Command) any {
	t.Helper()
	b, err := EncodeCommand(c)
	if err != nil {
		t.Fatal(err)
	}
	return k.Apply(&raft.Log{Data: b})
}

// memSink is a raft.SnapshotSink backed by a buffer.
type memSink struct {
	bytes.Buffer
	cancelled bool
}

func (s *memSink) ID() string    { return "mem" }
func (s *memSink) Cancel() error { s.cancelled = true; return nil }
func (s *memSink) Close() error  { return nil }

func snapshotBytes(t *testing.T, k *KV) *memSink {
	t.Helper()
	snap, err := k.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	sink := &memSink{}
	if err := snap.Persist(sink); err != nil {
		t.Fatal(err)
	}
	snap.Release()
	return sink
}

func TestApplySetThenGet(t *testing.T) {
	k := New()
	if res := apply(t, k, Command{Op: OpSet, Key: "a", Value: "1"}); res != nil {
		t.Fatalf("Apply returned %v, want nil", res)
	}
	if v, ok := k.Get("a"); !ok || v != "1" {
		t.Fatalf("Get(a) = %q,%v want \"1\",true", v, ok)
	}
}

func TestApplyDeleteRemovesKey(t *testing.T) {
	k := New()
	apply(t, k, Command{Op: OpSet, Key: "a", Value: "1"})
	apply(t, k, Command{Op: OpDelete, Key: "a"})
	if _, ok := k.Get("a"); ok {
		t.Fatal("key still present after delete")
	}
}

func TestApplyBadCommandReturnsErrorAndKeepsState(t *testing.T) {
	k := New()
	apply(t, k, Command{Op: OpSet, Key: "keep", Value: "me"})

	if _, isErr := apply(t, k, Command{Op: "nope", Key: "keep"}).(error); !isErr {
		t.Fatal("unknown op: want error result")
	}
	if _, isErr := k.Apply(&raft.Log{Data: []byte("not json")}).(error); !isErr {
		t.Fatal("non-JSON: want error result")
	}
	if v, ok := k.Get("keep"); !ok || v != "me" {
		t.Fatalf("state changed by bad commands: %q,%v", v, ok)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	k := New()
	for i := 0; i < 100; i++ {
		apply(t, k, Command{Op: OpSet, Key: fmt.Sprintf("k%d", i), Value: fmt.Sprintf("v%d", i)})
	}
	sink := snapshotBytes(t, k)

	r := New()
	if err := r.Restore(io.NopCloser(&sink.Buffer)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if v, ok := r.Get(fmt.Sprintf("k%d", i)); !ok || v != fmt.Sprintf("v%d", i) {
			t.Fatalf("k%d = %q,%v after restore", i, v, ok)
		}
	}
}

func TestSnapshotIsolatedFromLaterWrites(t *testing.T) {
	k := New()
	apply(t, k, Command{Op: OpSet, Key: "k", Value: "old"})
	snap, err := k.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	apply(t, k, Command{Op: OpSet, Key: "k", Value: "new"})

	sink := &memSink{}
	if err := snap.Persist(sink); err != nil {
		t.Fatal(err)
	}
	r := New()
	if err := r.Restore(io.NopCloser(&sink.Buffer)); err != nil {
		t.Fatal(err)
	}
	if v, _ := r.Get("k"); v != "old" {
		t.Fatalf("snapshot saw later write: k = %q, want \"old\"", v)
	}
}
