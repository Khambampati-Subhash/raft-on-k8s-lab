package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestResolveBootstrap(t *testing.T) {
	cases := []struct {
		mode, id string
		want     bool
		wantErr  bool
	}{
		{"auto", "kv-0", true, false},
		{"auto", "kv-1", false, false},
		{"auto", "node-10", false, false},
		{"true", "kv-7", true, false},
		{"false", "kv-0", false, false},
		{"bogus", "kv-0", false, true},
	}
	for _, tc := range cases {
		got, err := resolveBootstrap(tc.mode, tc.id)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("resolveBootstrap(%q,%q) = %v,%v want %v,err=%v", tc.mode, tc.id, got, err, tc.want, tc.wantErr)
		}
	}
}

type fakeJoiner struct {
	failures int
	calls    int
}

func (f *fakeJoiner) Join(context.Context, string, string) error {
	f.calls++
	if f.calls <= f.failures {
		return errors.New("not yet")
	}
	return nil
}

var fast = Backoff{Initial: time.Millisecond, Max: 4 * time.Millisecond}

func TestJoinLoopRetriesUntilSuccess(t *testing.T) {
	j := &fakeJoiner{failures: 3}
	joinLoop(context.Background(), j, "kv-1", "kv-1:7000", func() bool { return false }, fast)
	if j.calls != 4 {
		t.Fatalf("calls = %d, want 4", j.calls)
	}
}

func TestJoinLoopStopsWhenAlreadyMember(t *testing.T) {
	j := &fakeJoiner{}
	joinLoop(context.Background(), j, "kv-1", "kv-1:7000", func() bool { return true }, fast)
	if j.calls != 0 {
		t.Fatalf("calls = %d, want 0", j.calls)
	}
}

func TestJoinLoopStopsOnCancel(t *testing.T) {
	j := &fakeJoiner{failures: 1 << 30}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	joinLoop(ctx, j, "kv-1", "kv-1:7000", func() bool { return false }, fast)
	if ctx.Err() == nil {
		t.Fatal("joinLoop returned before ctx was done")
	}
}
