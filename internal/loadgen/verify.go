package loadgen

import (
	"context"
	"time"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/client"
)

// Reader is a linearizable read (client.Cluster.Get satisfies it).
type Reader interface {
	Get(ctx context.Context, key string) (string, bool, error)
}

const retryPause = 50 * time.Millisecond

// Verify reads back every ok and indeterminate write. An acknowledged write
// that is missing or has the wrong value is Lost. A read that keeps failing
// for retryFor is a VerifyError, never Lost: we can't prove it either way.
// Once one read has exhausted retryFor, later reads get a single attempt so
// an unreachable cluster costs retryFor once, not once per key.
func Verify(ctx context.Context, recs []Record, r Reader, retryFor time.Duration) Report {
	rep := Report{Total: len(recs)}
	gaveUp := false
	for _, rec := range recs {
		switch rec.Outcome {
		case client.Failed:
			rep.Failed++
			continue
		case client.OK:
			rep.OK++
		default:
			rep.Indeterminate++
		}
		budget := retryFor
		if gaveUp {
			budget = 0
		}
		v, found, err := readRetry(ctx, r, rec.Key, budget)
		if err != nil {
			gaveUp = true
			rep.VerifyErrors++
			continue
		}
		present := found && v == rec.Value
		switch {
		case rec.Outcome == client.OK && !present:
			rep.Lost++
			rep.LostKeys = append(rep.LostKeys, rec.Key)
		case rec.Outcome == client.Indeterminate && present:
			rep.IndeterminatePresent++
		case rec.Outcome == client.Indeterminate:
			rep.IndeterminateAbsent++
		}
	}
	return rep
}

func readRetry(ctx context.Context, r Reader, key string, budget time.Duration) (string, bool, error) {
	deadline := time.Now().Add(budget)
	for {
		v, found, err := r.Get(ctx, key)
		if err == nil || !time.Now().Add(retryPause).Before(deadline) {
			return v, found, err
		}
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		case <-time.After(retryPause):
		}
	}
}
