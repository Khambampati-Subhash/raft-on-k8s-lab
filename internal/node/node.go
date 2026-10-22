// Package node wraps hashicorp/raft and the KV state machine into the small
// Store the HTTP API talks to. All Raft knowledge lives here.
package node

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/api"
	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/fsm"
)

const (
	defaultApplyTimeout      = 5 * time.Second
	defaultSnapshotThreshold = 1024
)

// Config tunes one node. Zero durations and counts mean "library default"
// (ApplyTimeout: 5s, SnapshotThreshold: 1024).
type Config struct {
	ID        string
	Bootstrap bool // bootstrap a 1-node cluster if there is no existing state

	HeartbeatTimeout   time.Duration
	ElectionTimeout    time.Duration
	LeaderLeaseTimeout time.Duration
	CommitTimeout      time.Duration
	ApplyTimeout       time.Duration

	SnapshotThreshold uint64
	TrailingLogs      uint64

	LogOutput io.Writer // raft's own logs; nil = stderr
}

// Node is one Raft member plus its state machine.
type Node struct {
	cfg  Config
	raft *raft.Raft
	kv   *fsm.KV

	// barrierTerm is the last term in which this node, as leader, has applied
	// a barrier. See Get.
	barrierTerm atomic.Uint64
}

func New(cfg Config, d Deps) (*Node, error) {
	if cfg.ApplyTimeout == 0 {
		cfg.ApplyTimeout = defaultApplyTimeout
	}
	if cfg.LogOutput == nil {
		cfg.LogOutput = os.Stderr
	}
	rc := raft.DefaultConfig()
	rc.LocalID = raft.ServerID(cfg.ID)
	rc.Logger = hclog.New(&hclog.LoggerOptions{Name: "raft", Output: cfg.LogOutput, Level: hclog.Info})
	setDur(&rc.HeartbeatTimeout, cfg.HeartbeatTimeout)
	setDur(&rc.ElectionTimeout, cfg.ElectionTimeout)
	setDur(&rc.LeaderLeaseTimeout, cfg.LeaderLeaseTimeout)
	setDur(&rc.CommitTimeout, cfg.CommitTimeout)
	rc.SnapshotThreshold = defaultSnapshotThreshold
	if cfg.SnapshotThreshold != 0 {
		rc.SnapshotThreshold = cfg.SnapshotThreshold
	}
	if cfg.TrailingLogs != 0 {
		rc.TrailingLogs = cfg.TrailingLogs
	}

	kv := fsm.New()
	r, err := raft.NewRaft(rc, kv, d.Logs, d.Stable, d.Snaps, d.Transport)
	if err != nil {
		return nil, fmt.Errorf("start raft: %w", err)
	}

	if cfg.Bootstrap {
		has, err := raft.HasExistingState(d.Logs, d.Stable, d.Snaps)
		if err != nil {
			r.Shutdown()
			return nil, fmt.Errorf("check existing state: %w", err)
		}
		if !has {
			boot := raft.Configuration{Servers: []raft.Server{{ID: rc.LocalID, Address: d.Transport.LocalAddr()}}}
			if err := r.BootstrapCluster(boot).Error(); err != nil {
				r.Shutdown()
				return nil, fmt.Errorf("bootstrap: %w", err)
			}
		}
	}
	return &Node{cfg: cfg, raft: r, kv: kv}, nil
}

func setDur(dst *time.Duration, v time.Duration) {
	if v != 0 {
		*dst = v
	}
}

func (n *Node) Set(ctx context.Context, key, value string) error {
	return n.apply(ctx, fsm.Command{Op: fsm.OpSet, Key: key, Value: value})
}

func (n *Node) Delete(ctx context.Context, key string) error {
	return n.apply(ctx, fsm.Command{Op: fsm.OpDelete, Key: key})
}

func (n *Node) apply(ctx context.Context, c fsm.Command) error {
	b, err := fsm.EncodeCommand(c)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, n.cfg.ApplyTimeout)
	defer cancel()
	f := n.raft.Apply(b, remaining(ctx))
	if err := wait(ctx, f); err != nil {
		return n.mapErr(err)
	}
	if ferr, ok := f.Response().(error); ok {
		return ferr
	}
	return nil
}

// Get reads a key. With stale=true it reads this node's state machine as-is,
// which may be behind. Otherwise it performs a linearizable read
// ("ReadIndex"): the leader makes sure it has applied everything committed
// before its term began (a barrier, once per term), confirms with a quorum that
// it is still leader, then waits until its state machine has caught up to the
// commit index it saw before confirming.
func (n *Node) Get(ctx context.Context, key string, stale bool) (string, bool, error) {
	if !stale {
		ctx, cancel := context.WithTimeout(ctx, n.cfg.ApplyTimeout)
		defer cancel()
		if err := n.readIndex(ctx); err != nil {
			return "", false, err
		}
	}
	v, ok := n.kv.Get(key)
	return v, ok, nil
}

func (n *Node) readIndex(ctx context.Context) error {
	if n.raft.State() != raft.Leader {
		return n.mapErr(raft.ErrNotLeader)
	}
	if term := n.raft.CurrentTerm(); n.barrierTerm.Load() != term {
		if err := wait(ctx, n.raft.Barrier(remaining(ctx))); err != nil {
			return n.mapErr(err)
		}
		n.barrierTerm.Store(term)
	}
	commit := n.raft.CommitIndex()
	if err := wait(ctx, n.raft.VerifyLeader()); err != nil {
		return n.mapErr(err)
	}
	for n.raft.AppliedIndex() < commit {
		select {
		case <-ctx.Done():
			return ErrTimeout
		case <-time.After(time.Millisecond):
		}
	}
	return nil
}

// wait blocks on a raft future, giving up when ctx ends. Raft futures can
// block long after a leader loses quorum, so ctx is what bounds the call.
func wait(ctx context.Context, f raft.Future) error {
	done := make(chan error, 1)
	go func() { done <- f.Error() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("%w: %v", ErrTimeout, ctx.Err())
	}
}

func remaining(ctx context.Context) time.Duration {
	if dl, ok := ctx.Deadline(); ok {
		return time.Until(dl)
	}
	return 0
}

func (n *Node) Status() api.Status {
	addr, id := n.raft.LeaderWithID()
	st := api.Status{
		ID:           n.cfg.ID,
		Role:         strings.ToLower(n.raft.State().String()),
		Term:         n.raft.CurrentTerm(),
		LeaderID:     string(id),
		LeaderAddr:   string(addr),
		CommitIndex:  n.raft.CommitIndex(),
		AppliedIndex: n.raft.AppliedIndex(),
		LastLogIndex: n.raft.LastIndex(),
		LastContact:  n.raft.Stats()["last_contact"],
	}
	if f := n.raft.GetConfiguration(); f.Error() == nil {
		for _, s := range f.Configuration().Servers {
			st.Servers = append(st.Servers, api.Server{ID: string(s.ID), Addr: string(s.Address), Suffrage: s.Suffrage.String()})
		}
	}
	return st
}

func (n *Node) Shutdown() error { return n.raft.Shutdown().Error() }

