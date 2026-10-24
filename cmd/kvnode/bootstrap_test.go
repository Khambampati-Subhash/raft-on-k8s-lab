package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/api"
	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/client"
)

func peer(id string, term uint64) client.StatusResult {
	return client.StatusResult{URL: "http://" + id, Status: &api.Status{ID: id, Term: term}}
}

func down(url string) client.StatusResult {
	return client.StatusResult{URL: url, Err: errors.New("connection refused")}
}

func TestDecideBootstrap(t *testing.T) {
	for _, tc := range []struct {
		name  string
		peers []client.StatusResult
		want  decision
	}{
		{"no peers configured", nil, bootstrapNow},
		{"all peers up and empty (fresh cluster)", []client.StatusResult{peer("kv-1", 0), peer("kv-2", 0)}, bootstrapNow},
		{"a peer has a term: a cluster exists", []client.StatusResult{peer("kv-1", 3), peer("kv-2", 0)}, joinExisting},
		{"a peer has a term, another unreachable", []client.StatusResult{peer("kv-1", 3), down("http://kv-2")}, joinExisting},
		{"a peer unreachable, none with a term", []client.StatusResult{peer("kv-1", 0), down("http://kv-2")}, waitForPeers},
		{"all peers unreachable", []client.StatusResult{down("http://kv-1"), down("http://kv-2")}, waitForPeers},
	} {
		if got := decideBootstrap(tc.peers); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

type scriptedProber struct {
	rounds [][]client.StatusResult
	calls  int
}

func (p *scriptedProber) Statuses(context.Context) []client.StatusResult {
	r := p.rounds[min(p.calls, len(p.rounds)-1)]
	p.calls++
	return r
}

func TestAwaitBootstrapWaitsThenJoins(t *testing.T) {
	p := &scriptedProber{rounds: [][]client.StatusResult{
		{down("http://kv-1"), down("http://kv-2")},
		{down("http://kv-1"), down("http://kv-2")},
		{peer("kv-1", 4), down("http://kv-2")},
	}}
	if awaitBootstrapDecision(context.Background(), p, time.Millisecond) {
		t.Fatal("bootstrapped although a peer reported a term")
	}
	if p.calls != 3 {
		t.Fatalf("probed %d times, want 3", p.calls)
	}
}

func TestAwaitBootstrapFreshCluster(t *testing.T) {
	p := &scriptedProber{rounds: [][]client.StatusResult{
		{down("http://kv-1"), peer("kv-2", 0)},
		{peer("kv-1", 0), peer("kv-2", 0)},
	}}
	if !awaitBootstrapDecision(context.Background(), p, time.Millisecond) {
		t.Fatal("did not bootstrap a fresh cluster once every peer answered")
	}
}

func TestAwaitBootstrapStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &scriptedProber{rounds: [][]client.StatusResult{{down("http://kv-1")}}}
	if awaitBootstrapDecision(ctx, p, time.Millisecond) {
		t.Fatal("bootstrapped after cancel")
	}
}

func TestOtherPeersDropsSelf(t *testing.T) {
	got := otherPeers([]string{"http://kv-0.kv.raftlab.svc.cluster.local:8080", "http://kv-1.kv.raftlab.svc.cluster.local:8080", "http://kv-10.kv:8080"}, "kv-1")
	if len(got) != 2 || got[0] != "http://kv-0.kv.raftlab.svc.cluster.local:8080" || got[1] != "http://kv-10.kv:8080" {
		t.Fatalf("otherPeers = %v", got)
	}
}
