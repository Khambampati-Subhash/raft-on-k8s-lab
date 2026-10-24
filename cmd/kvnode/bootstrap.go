package main

import (
	"context"
	"time"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/client"
)

// decision is what an empty, bootstrap-eligible node does next.
type decision int

const (
	bootstrapNow decision = iota // every peer answered and none has ever been in a cluster
	joinExisting                 // some peer has a term: a cluster exists, join it
	waitForPeers                 // can't tell yet: some peer didn't answer
)

func (d decision) String() string {
	return [...]string{"bootstrap", "join", "wait"}[d]
}

// decideBootstrap looks at the other members' /status. Bootstrapping is only
// safe when we can *see* there is no cluster: every peer reachable and none
// with a term. An unreachable peer might be part of a live cluster that just
// can't hear us — bootstrapping then creates a second cluster (E9c lost
// 170,972 acknowledged writes that way), so we wait instead.
func decideBootstrap(peers []client.StatusResult) decision {
	unreachable := false
	for _, p := range peers {
		switch {
		case p.Status == nil:
			unreachable = true
		case p.Status.Term > 0:
			return joinExisting
		}
	}
	if unreachable {
		return waitForPeers
	}
	return bootstrapNow
}

// Prober lists the other members' /status (client.Cluster satisfies it).
type Prober interface {
	Statuses(ctx context.Context) []client.StatusResult
}

// awaitBootstrapDecision probes every `every` until the answer is bootstrap
// (true) or join (false). It returns false if ctx ends first.
func awaitBootstrapDecision(ctx context.Context, p Prober, every time.Duration) bool {
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		d := decideBootstrap(p.Statuses(probeCtx))
		cancel()
		if d != waitForPeers {
			return d == bootstrapNow
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(every):
		}
	}
}
