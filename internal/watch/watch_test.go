package watch

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/api"
	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/client"
)

func up(id, role string, term uint64, leader string) client.StatusResult {
	return client.StatusResult{URL: "http://" + id, Status: &api.Status{ID: id, Role: role, Term: term, LeaderID: leader}}
}

func ofKind(evs []Event, kind string) []Event {
	var out []Event
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

var now = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func TestDiffLeaderChange(t *testing.T) {
	prev := []client.StatusResult{up("kv-0", "leader", 2, "kv-0"), up("kv-1", "follower", 2, "kv-0")}
	cur := []client.StatusResult{up("kv-0", "follower", 3, "kv-1"), up("kv-1", "leader", 3, "kv-1")}

	lc := ofKind(Diff(prev, cur, now), "leader-change")
	if len(lc) != 1 || lc[0].Detail != "kv-0 -> kv-1" || lc[0].Node != "kv-1" {
		t.Fatalf("leader-change events = %+v, want one kv-0 -> kv-1", lc)
	}
}

func TestDiffTermBump(t *testing.T) {
	prev := []client.StatusResult{up("kv-0", "follower", 2, "kv-1")}
	cur := []client.StatusResult{up("kv-0", "candidate", 3, "")}

	evs := Diff(prev, cur, now)
	if tb := ofKind(evs, "term"); len(tb) != 1 || tb[0].Detail != "2 -> 3" {
		t.Fatalf("term events = %+v, want one 2 -> 3", tb)
	}
	if r := ofKind(evs, "role"); len(r) != 1 || r[0].Detail != "follower -> candidate" {
		t.Fatalf("role events = %+v", r)
	}
}

func TestDiffUnreachableThenReachable(t *testing.T) {
	both := []client.StatusResult{up("kv-0", "leader", 2, "kv-0"), up("kv-1", "follower", 2, "kv-0")}
	one := []client.StatusResult{up("kv-0", "leader", 2, "kv-0"), {URL: "http://kv-1", Err: errFake}}

	if u := ofKind(Diff(both, one, now), "unreachable"); len(u) != 1 || u[0].Node != "kv-1" {
		t.Fatalf("unreachable events = %+v", u)
	}
	if r := ofKind(Diff(one, both, now), "reachable"); len(r) != 1 || r[0].Node != "kv-1" {
		t.Fatalf("reachable events = %+v", r)
	}
}

func TestDiffNoChangeNoEvents(t *testing.T) {
	s := []client.StatusResult{up("kv-0", "leader", 2, "kv-0"), up("kv-1", "follower", 2, "kv-0")}
	if evs := Diff(s, s, now); len(evs) != 0 {
		t.Fatalf("events = %+v, want none", evs)
	}
	if evs := Diff(nil, s, now); len(evs) != 0 {
		t.Fatalf("first poll events = %+v, want none", evs)
	}
}

func TestRenderContainsAllNodes(t *testing.T) {
	var b bytes.Buffer
	Render(&b, []client.StatusResult{up("kv-0", "leader", 2, "kv-0"), up("kv-1", "follower", 2, "kv-0"), {URL: "http://10.0.0.9:8080", Err: errFake}})
	out := b.String()
	for _, want := range []string{"kv-0", "kv-1", "leader", "follower", "10.0.0.9", "unreachable"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
}

type fakeErr struct{}

func (fakeErr) Error() string { return "connection refused" }

var errFake error = fakeErr{}
