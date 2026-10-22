package node

import (
	"github.com/hashicorp/raft"
)

// Join adds id at addr as a voter. It must be called on the leader.
// Joining again with the same id and addr is a no-op, so a restarted node can
// always ask to join. If id or addr already belong to a different member, that
// stale entry is removed first.
func (n *Node) Join(id, addr string) error {
	if n.raft.State() != raft.Leader {
		return n.mapErr(raft.ErrNotLeader)
	}
	f := n.raft.GetConfiguration()
	if err := f.Error(); err != nil {
		return n.mapErr(err)
	}
	sid, saddr := raft.ServerID(id), raft.ServerAddress(addr)
	for _, s := range f.Configuration().Servers {
		if s.ID == sid && s.Address == saddr {
			return nil
		}
		if s.ID == sid || s.Address == saddr {
			if err := n.raft.RemoveServer(s.ID, 0, 0).Error(); err != nil {
				return n.mapErr(err)
			}
		}
	}
	return n.mapErr(n.raft.AddVoter(sid, saddr, 0, 0).Error())
}

// Leave removes id from the configuration. It must be called on the leader.
func (n *Node) Leave(id string) error {
	if n.raft.State() != raft.Leader {
		return n.mapErr(raft.ErrNotLeader)
	}
	return n.mapErr(n.raft.RemoveServer(raft.ServerID(id), 0, 0).Error())
}
