// Package loadgen drives a write workload against the cluster and then checks
// that every acknowledged write is still there.
package loadgen

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/client"
)

// Writer is the write side of the cluster (client.Cluster satisfies it).
type Writer interface {
	Put(ctx context.Context, key, value string) (client.Outcome, error)
}

// Record is one write attempt and what the client could say about it.
type Record struct {
	Key, Value string
	Outcome    client.Outcome
	Start, End time.Time
	Err        string
}

// backoff is how long a worker pauses after a write that wasn't ok. Without
// it, a dead leader refuses connections instantly and workers spin, turning a
// 3s election into tens of thousands of "failed" writes.
const backoff = 20 * time.Millisecond

// Run starts workers that write unique keys <runID>/<worker>/<seq> for d.
// Writes in flight when d expires are allowed to finish, so their outcome is
// still recorded honestly instead of being cut off as indeterminate.
func Run(ctx context.Context, w Writer, workers int, d time.Duration, runID string) []Record {
	deadline := time.Now().Add(d)
	per := make([][]Record, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for seq := 0; time.Now().Before(deadline) && ctx.Err() == nil; seq++ {
				r := write(ctx, w, fmt.Sprintf("%s/%d/%d", runID, i, seq))
				per[i] = append(per[i], r)
				if r.Outcome != client.OK {
					time.Sleep(backoff)
				}
			}
		}()
	}
	wg.Wait()
	var out []Record
	for _, rs := range per {
		out = append(out, rs...)
	}
	return out
}

func write(ctx context.Context, w Writer, key string) Record {
	r := Record{Key: key, Value: randomValue(), Start: time.Now()}
	o, err := w.Put(ctx, r.Key, r.Value)
	r.End, r.Outcome = time.Now(), o
	if err != nil {
		r.Err = err.Error()
	}
	return r
}

func randomValue() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// StaleReader can list the nodes and read one of them without the leader.
type StaleReader interface {
	Statuses(ctx context.Context) []client.StatusResult
	GetStale(ctx context.Context, nodeURL, key string) (string, bool, error)
}

// Probe writes an increasing counter to key every writeEvery and reads it
// stale from every node every readEvery, until ctx ends. It returns the max
// lag (last acknowledged counter − counter a node returned) seen per node ID.
func Probe(ctx context.Context, w Writer, r StaleReader, key string, writeEvery, readEvery time.Duration) map[string]uint64 {
	var mu sync.Mutex
	var acked uint64
	lag := map[string]uint64{}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(writeEvery)
		defer t.Stop()
		for n := uint64(1); ; n++ {
			if o, _ := w.Put(ctx, key, strconv.FormatUint(n, 10)); o == client.OK {
				mu.Lock()
				acked = max(acked, n)
				mu.Unlock()
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	t := time.NewTicker(readEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return lag
		case <-t.C:
		}
		mu.Lock()
		want := acked // snapshot before reading: lag is never overstated
		mu.Unlock()
		for _, s := range r.Statuses(ctx) {
			if s.Status == nil {
				continue
			}
			v, found, err := r.GetStale(ctx, s.URL, key)
			if err != nil {
				continue
			}
			var got uint64
			if found {
				if got, err = strconv.ParseUint(v, 10, 64); err != nil {
					continue
				}
			}
			var l uint64 // 0 is recorded too: "read, in sync" differs from "never read"
			if want > got {
				l = want - got
			}
			lag[s.Status.ID] = max(lag[s.Status.ID], l)
		}
	}
}
