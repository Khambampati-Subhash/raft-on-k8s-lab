package node

import (
	"errors"
	"fmt"

	"github.com/hashicorp/raft"
)

// ErrNoLeader means the request was definitely not applied: there is no
// leader right now, or this node could not accept the request.
var ErrNoLeader = errors.New("no leader")

// ErrTimeout means the outcome is unknown: the write may or may not commit.
// Callers must not treat it as a failure.
var ErrTimeout = errors.New("outcome unknown")

// NotLeaderError means this node is a follower; LeaderID is who to ask instead.
// The request was definitely not applied.
type NotLeaderError struct{ LeaderID string }

func (e *NotLeaderError) Error() string { return "not leader; leader is " + e.LeaderID }

// mapErr turns raft errors into the three outcomes the API exposes.
func (n *Node) mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, raft.ErrNotLeader):
		if _, id := n.raft.LeaderWithID(); id != "" {
			return &NotLeaderError{LeaderID: string(id)}
		}
		return ErrNoLeader
	case errors.Is(err, raft.ErrLeadershipLost):
		// The entry may already be on a majority and commit under the next leader.
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	case errors.Is(err, raft.ErrEnqueueTimeout),
		errors.Is(err, raft.ErrLeadershipTransferInProgress),
		errors.Is(err, raft.ErrRaftShutdown):
		return fmt.Errorf("%w: %v", ErrNoLeader, err)
	default:
		return err
	}
}
