package node

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSingleNodeBootstrapBecomesLeader(t *testing.T) {
	d, _ := inmemDeps("solo")
	cfg := fastConfig("solo")
	cfg.Bootstrap = true
	n, err := New(cfg, d)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Shutdown()

	eventually(t, "solo becomes leader", func() bool { return n.Status().Role == "leader" })
	if st := n.Status(); st.Term < 1 || st.LeaderID != "solo" {
		t.Fatalf("status = %+v, want term>=1 and leaderId=solo", st)
	}
}

func TestReplicationToFollowers(t *testing.T) {
	c := newTestCluster(t, 3, nil)
	if err := c.leader().Set(context.Background(), "k", "v"); err != nil {
		t.Fatal(err)
	}
	for _, f := range c.followers() {
		eventually(t, "follower has k", func() bool {
			v, ok, err := f.Get(context.Background(), "k", true)
			return err == nil && ok && v == "v"
		})
	}
}

func TestWriteOnFollowerReturnsNotLeader(t *testing.T) {
	c := newTestCluster(t, 3, nil)
	leaderID := c.leader().Status().ID
	f := c.followers()[0]
	eventually(t, "follower knows leader", func() bool { return f.Status().LeaderID != "" })

	err := f.Set(context.Background(), "k", "v")
	var nle *NotLeaderError
	if !errors.As(err, &nle) || nle.LeaderID != leaderID {
		t.Fatalf("err = %v, want NotLeaderError{%s}", err, leaderID)
	}
}

func TestLinearizableGetOnFollowerReturnsNotLeader(t *testing.T) {
	c := newTestCluster(t, 3, nil)
	c.leader()
	f := c.followers()[0]
	eventually(t, "follower knows leader", func() bool { return f.Status().LeaderID != "" })

	_, _, err := f.Get(context.Background(), "k", false)
	var nle *NotLeaderError
	if !errors.As(err, &nle) {
		t.Fatalf("err = %v, want NotLeaderError", err)
	}
}

func TestStaleGetOnFollowerSucceeds(t *testing.T) {
	c := newTestCluster(t, 3, nil)
	c.leader().Set(context.Background(), "k", "v")
	f := c.followers()[0]
	eventually(t, "stale read sees k", func() bool {
		v, ok, err := f.Get(context.Background(), "k", true)
		return err == nil && ok && v == "v"
	})
}

func TestLeaderFailover(t *testing.T) {
	c := newTestCluster(t, 3, nil)
	old := c.leader()
	oldTerm := old.Status().Term
	if err := old.Set(context.Background(), "k", "v"); err != nil {
		t.Fatal(err)
	}
	oldIdx := c.indexOf(old)
	c.stop(oldIdx)

	nl := c.leader(oldIdx)
	if nl == old {
		t.Fatal("stopped node still leader")
	}
	v, ok, err := nl.Get(context.Background(), "k", false)
	if err != nil || !ok || v != "v" {
		t.Fatalf("Get on new leader = %q,%v,%v; acked write lost", v, ok, err)
	}
	if nl.Status().Term <= oldTerm {
		t.Fatalf("new term %d not greater than old %d", nl.Status().Term, oldTerm)
	}
}

func TestIsolatedLeaderWriteNeverReportsSuccess(t *testing.T) {
	c := newTestCluster(t, 3, nil)
	l := c.leader()
	c.isolate(c.indexOf(l))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := l.Set(ctx, "k", "v")
	var nle *NotLeaderError
	if err == nil {
		t.Fatal("isolated leader acknowledged a write it cannot commit")
	}
	if !errors.Is(err, ErrTimeout) && !errors.Is(err, ErrNoLeader) && !errors.As(err, &nle) {
		t.Fatalf("err = %v, want ErrTimeout, ErrNoLeader or NotLeaderError", err)
	}
}

func TestRestartWithExistingStateKeepsDataAndTerm(t *testing.T) {
	d, _ := inmemDeps("solo")
	cfg := fastConfig("solo")
	cfg.Bootstrap = true
	n, err := New(cfg, d)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "leader", func() bool { return n.Status().Role == "leader" })
	if err := n.Set(context.Background(), "k", "v"); err != nil {
		t.Fatal(err)
	}
	term := n.Status().Term
	n.Shutdown()

	// Same stores, Bootstrap still true: must not re-bootstrap or error.
	_, tr := inmemDeps("solo")
	d.Transport = tr
	n2, err := New(cfg, d)
	if err != nil {
		t.Fatalf("restart with existing state: %v", err)
	}
	defer n2.Shutdown()
	eventually(t, "leader after restart", func() bool { return n2.Status().Role == "leader" })
	if v, ok, err := n2.Get(context.Background(), "k", false); err != nil || !ok || v != "v" {
		t.Fatalf("after restart Get = %q,%v,%v", v, ok, err)
	}
	if n2.Status().Term < term {
		t.Fatalf("term went backwards: %d < %d", n2.Status().Term, term)
	}
}
