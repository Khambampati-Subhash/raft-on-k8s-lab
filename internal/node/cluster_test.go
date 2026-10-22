package node

import (
	"fmt"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// testCluster runs n nodes in one process over in-memory transports, with
// timeouts shrunk so elections take milliseconds instead of seconds.
type testCluster struct {
	t      *testing.T
	nodes  []*Node
	trans  []*raft.InmemTransport
	deps   []Deps
	closed []bool
}

func fastConfig(id string) Config {
	return Config{
		ID:                 id,
		HeartbeatTimeout:   50 * time.Millisecond,
		ElectionTimeout:    50 * time.Millisecond,
		LeaderLeaseTimeout: 50 * time.Millisecond,
		CommitTimeout:      5 * time.Millisecond,
		ApplyTimeout:       2 * time.Second,
	}
}

func inmemDeps(id string) (Deps, *raft.InmemTransport) {
	_, tr := raft.NewInmemTransport(raft.ServerAddress(id))
	store := raft.NewInmemStore()
	return Deps{Logs: store, Stable: store, Snaps: raft.NewInmemSnapshotStore(), Transport: tr}, tr
}

// newTestCluster starts n nodes. Node 0 bootstraps alone; the others start
// empty and are added with Join, the same way kvnode does it.
func newTestCluster(t *testing.T, n int, tweak func(*Config)) *testCluster {
	t.Helper()
	c := &testCluster{t: t}
	t.Cleanup(c.shutdownAll)
	d, tr := inmemDeps("n0")
	cfg := fastConfig("n0")
	cfg.Bootstrap = true
	if tweak != nil {
		tweak(&cfg)
	}
	n0, err := New(cfg, d)
	if err != nil {
		t.Fatal(err)
	}
	c.nodes, c.trans, c.deps, c.closed = []*Node{n0}, []*raft.InmemTransport{tr}, []Deps{d}, []bool{false}
	c.leader()
	for i := 1; i < n; i++ {
		id := fmt.Sprintf("n%d", i)
		c.spawn(id, tweak)
		if err := c.leader().Join(id, id); err != nil {
			t.Fatalf("join %s: %v", id, err)
		}
	}
	return c
}

func (c *testCluster) shutdownAll() {
	for i, n := range c.nodes {
		if !c.closed[i] {
			n.Shutdown()
			c.closed[i] = true
		}
	}
}

// stop shuts node i down and cuts it off from the others.
func (c *testCluster) stop(i int) {
	c.nodes[i].Shutdown()
	c.closed[i] = true
	c.isolate(i)
}

// isolate cuts every link to and from node i without stopping it.
func (c *testCluster) isolate(i int) {
	c.trans[i].DisconnectAll()
	for j := range c.trans {
		if j != i {
			c.trans[j].Disconnect(c.trans[i].LocalAddr())
		}
	}
}

// reconnect restores links between node i and every other node.
func (c *testCluster) reconnect(i int) {
	for j := range c.trans {
		if j != i {
			c.trans[i].Connect(c.trans[j].LocalAddr(), c.trans[j])
			c.trans[j].Connect(c.trans[i].LocalAddr(), c.trans[i])
		}
	}
}

// leader waits up to 3s for exactly one running node to report itself leader.
func (c *testCluster) leader(except ...int) *Node {
	c.t.Helper()
	skip := map[int]bool{}
	for _, e := range except {
		skip[e] = true
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for i, n := range c.nodes {
			if !c.closed[i] && !skip[i] && n.Status().Role == "leader" {
				return n
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.t.Fatal("no leader within 3s")
	return nil
}

func (c *testCluster) followers() []*Node {
	var out []*Node
	for i, n := range c.nodes {
		if !c.closed[i] && n.Status().Role == "follower" {
			out = append(out, n)
		}
	}
	return out
}

func (c *testCluster) indexOf(n *Node) int {
	for i, m := range c.nodes {
		if m == n {
			return i
		}
	}
	return -1
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}
