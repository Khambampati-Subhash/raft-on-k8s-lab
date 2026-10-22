package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// resolveBootstrap decides whether this node bootstraps a new cluster.
// "auto" means: only the StatefulSet's first pod (name ending in "-0") does.
func resolveBootstrap(mode, id string) (bool, error) {
	switch mode {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "auto":
		return strings.HasSuffix(id, "-0"), nil
	default:
		return false, fmt.Errorf("--bootstrap must be auto, true or false, got %q", mode)
	}
}

// Joiner asks the cluster's leader to add a member.
type Joiner interface {
	Join(ctx context.Context, id, addr string) error
}

// Backoff is capped exponential: Initial, 2×Initial, ... up to Max.
type Backoff struct{ Initial, Max time.Duration }

// joinLoop asks to join until it succeeds, isMember reports true (we are
// already in the configuration, e.g. after a restart), or ctx ends.
func joinLoop(ctx context.Context, j Joiner, self, selfAddr string, isMember func() bool, b Backoff) {
	delay := b.Initial
	for {
		if isMember() {
			log.Printf("join: %s is already a member", self)
			return
		}
		err := j.Join(ctx, self, selfAddr)
		if err == nil {
			log.Printf("join: %s joined as %s", self, selfAddr)
			return
		}
		log.Printf("join: %v (retrying in %s)", err, delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(2*delay, b.Max)
	}
}
