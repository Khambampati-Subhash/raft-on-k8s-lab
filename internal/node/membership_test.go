package node

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// spawn starts a fresh, unbootstrapped node wired to every existing node and
// returns its index. It is not a member until someone calls Join.
func (c *testCluster) spawn(id string, tweak func(*Config)) int {
	c.t.Helper()
	d, tr := inmemDeps(id)
	for _, other := range c.trans {
		tr.Connect(other.LocalAddr(), other)
		other.Connect(tr.LocalAddr(), tr)
	}
	cfg := fastConfig(id)
	if tweak != nil {
		tweak(&cfg)
	}
	n, err := New(cfg, d)
	if err != nil {
		c.t.Fatal(err)
	}
	c.nodes = append(c.nodes, n)
	c.trans = append(c.trans, tr)
	c.deps = append(c.deps, d)
	c.closed = append(c.closed, false)
	return len(c.nodes) - 1
}

func servers(n *Node) map[string]string {
	out := map[string]string{}
	for _, s := range n.Status().Servers {
		out[s.ID] = s.Addr + "/" + s.Suffrage
	}
	return out
}

func TestJoinAddsVoter(t *testing.T) {
	c := newTestCluster(t, 1, nil)
	l := c.leader()
	b := c.spawn("b", nil)

	if err := l.Join("b", "b"); err != nil {
		t.Fatal(err)
	}
	if got := servers(l)["b"]; got != "b/Voter" {
		t.Fatalf("b in config = %q, want b/Voter", got)
	}
	l.Set(context.Background(), "k", "v")
	eventually(t, "b replicates k", func() bool {
		v, ok, _ := c.nodes[b].Get(context.Background(), "k", true)
		return ok && v == "v"
	})
}

func TestJoinIsIdempotent(t *testing.T) {
	c := newTestCluster(t, 1, nil)
	l := c.leader()
	c.spawn("b", nil)

	for i := 0; i < 2; i++ {
		if err := l.Join("b", "b"); err != nil {
			t.Fatalf("join #%d: %v", i+1, err)
		}
	}
	if n := len(l.Status().Servers); n != 2 {
		t.Fatalf("servers = %d, want 2 (b listed once)", n)
	}
}

func TestJoinSameIDNewAddrUpdates(t *testing.T) {
	c := newTestCluster(t, 1, nil)
	l := c.leader()
	c.spawn("b", nil)
	c.spawn("b2", nil) // a second transport address for the "same" member

	if err := l.Join("b", "b"); err != nil {
		t.Fatal(err)
	}
	if err := l.Join("b", "b2"); err != nil {
		t.Fatal(err)
	}
	s := servers(l)
	if len(s) != 2 || s["b"] != "b2/Voter" {
		t.Fatalf("servers = %v, want n0 + b at b2", s)
	}
}

func TestLeaveRemovesServer(t *testing.T) {
	c := newTestCluster(t, 3, nil)
	l := c.leader()
	f := c.followers()[0]
	fid := f.Status().ID

	if err := l.Leave(fid); err != nil {
		t.Fatal(err)
	}
	if _, ok := servers(l)[fid]; ok {
		t.Fatalf("%s still in config after Leave", fid)
	}
	c.stop(c.indexOf(f))
	if err := l.Set(context.Background(), "k", "v"); err != nil {
		t.Fatalf("2-member cluster cannot commit: %v", err)
	}
}

func TestJoinOnFollowerReturnsNotLeader(t *testing.T) {
	c := newTestCluster(t, 3, nil)
	c.leader()
	f := c.followers()[0]
	eventually(t, "follower knows leader", func() bool { return f.Status().LeaderID != "" })

	var nle *NotLeaderError
	if err := f.Join("x", "x"); !errors.As(err, &nle) {
		t.Fatalf("Join on follower err = %v, want NotLeaderError", err)
	}
	if err := f.Leave("x"); !errors.As(err, &nle) {
		t.Fatalf("Leave on follower err = %v, want NotLeaderError", err)
	}
}

func TestLaggingFollowerCatchesUpViaSnapshot(t *testing.T) {
	tweak := func(cfg *Config) { cfg.SnapshotThreshold = 10; cfg.TrailingLogs = 5 }
	c := newTestCluster(t, 3, tweak)
	l := c.leader()
	f := c.followers()[0]
	fi := c.indexOf(f)
	c.isolate(fi)

	for i := 0; i < 100; i++ {
		if err := l.Set(context.Background(), fmt.Sprintf("k%d", i), "v"); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.raft.Snapshot().Error(); err != nil {
		t.Fatal(err)
	}
	// The leader must have discarded the entries f is missing.
	if first, _ := c.deps[c.indexOf(l)].Logs.FirstIndex(); first <= 2 {
		t.Fatalf("leader log not compacted (first index %d); test would not exercise snapshots", first)
	}

	c.reconnect(fi)
	eventually(t, "follower restored all 100 keys", func() bool {
		for i := 0; i < 100; i++ {
			if _, ok, _ := f.Get(context.Background(), fmt.Sprintf("k%d", i), true); !ok {
				return false
			}
		}
		return true
	})
}
