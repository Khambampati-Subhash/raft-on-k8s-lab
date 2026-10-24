// Package api holds the wire types shared by the kvnode server and its clients
// (loadgen, raftwatch, raftctl). It has no dependencies on Raft.
package api

// HeaderLeader is set on 421 responses and carries the current leader's
// server ID, so a client can retry against the leader.
const HeaderLeader = "X-Raft-Leader"

// Server is one member of the Raft configuration.
type Server struct {
	ID       string `json:"id"`
	Addr     string `json:"addr"`
	Suffrage string `json:"suffrage"` // "Voter", "Nonvoter", "Staging"
}

// Status is one node's view of the cluster, served at GET /status.
type Status struct {
	ID           string   `json:"id"`
	Role         string   `json:"role"` // "leader" | "follower" | "candidate" | "shutdown"
	Term         uint64   `json:"term"`
	LeaderID     string   `json:"leaderId"`
	LeaderAddr   string   `json:"leaderAddr"`
	CommitIndex  uint64   `json:"commitIndex"`
	AppliedIndex uint64   `json:"appliedIndex"`
	LastLogIndex uint64   `json:"lastLogIndex"`
	LastContact  string   `json:"lastContact"` // raft Stats() value verbatim
	Servers      []Server `json:"servers"`
}

// JoinRequest is the body of POST /join.
type JoinRequest struct {
	ID   string `json:"id"`
	Addr string `json:"addr"`
}

// LeaveRequest is the body of POST /leave.
type LeaveRequest struct {
	ID string `json:"id"`
}

// StoreFault is the body of POST /debug/store-fault.
type StoreFault struct {
	Delay    string  `json:"delay,omitempty"` // Go duration, e.g. "200ms"; empty = 0
	FailRate float64 `json:"failRate,omitempty"`
}
