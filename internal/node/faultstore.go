package node

import (
	"errors"
	"math"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/hashicorp/raft"
)

// ErrInjectedIO is what a failed append returns while a fault is set.
var ErrInjectedIO = errors.New("injected I/O error (EIO)")

// FaultLogStore wraps a LogStore and can slow down or fail log appends
// (StoreLog/StoreLogs) at runtime. Reads, deletes, the stable store and
// snapshots are untouched. The delay applies once per call, so a batched
// StoreLogs pays it once.
//
// It embeds raft.LogStore, which hides any optional interfaces of the inner
// store. raft-boltdb/v2's BoltStore implements neither MonotonicLogStore nor
// CommitTrackingLogStore, so nothing is lost wrapping it.
type FaultLogStore struct {
	raft.LogStore
	Rand func() float64 // [0,1); set before use. Default: math/rand/v2.

	delay    atomic.Int64  // nanoseconds
	failRate atomic.Uint64 // math.Float64bits
}

func NewFaultLogStore(s raft.LogStore) *FaultLogStore {
	return &FaultLogStore{LogStore: s, Rand: rand.Float64}
}

// SetFault sets the per-append delay and the fraction of appends that fail.
// Zero values turn the fault off. Safe to call while raft is running.
func (f *FaultLogStore) SetFault(delay time.Duration, failRate float64) {
	f.delay.Store(int64(delay))
	f.failRate.Store(math.Float64bits(failRate))
}

func (f *FaultLogStore) inject() error {
	if d := time.Duration(f.delay.Load()); d > 0 {
		time.Sleep(d)
	}
	if r := math.Float64frombits(f.failRate.Load()); r > 0 && f.Rand() < r {
		return ErrInjectedIO
	}
	return nil
}

func (f *FaultLogStore) StoreLog(l *raft.Log) error {
	if err := f.inject(); err != nil {
		return err
	}
	return f.LogStore.StoreLog(l)
}

func (f *FaultLogStore) StoreLogs(ls []*raft.Log) error {
	if err := f.inject(); err != nil {
		return err
	}
	return f.LogStore.StoreLogs(ls)
}
