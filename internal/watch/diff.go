// Package watch turns successive /status polls into a table and a stream of
// events (leader changes, term bumps, nodes going away and coming back).
package watch

import (
	"fmt"
	"sort"
	"time"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/api"
	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/client"
)

// Event is one observed change. Kind is one of: "leader-change", "term",
// "role", "unreachable", "reachable".
type Event struct {
	At     time.Time
	Kind   string
	Node   string
	Detail string
}

func (e Event) String() string {
	return fmt.Sprintf("%s %-13s %-6s %s", e.At.Format(time.RFC3339Nano), e.Kind, e.Node, e.Detail)
}

// Diff compares two polls. Nodes are matched by ID, because in DNS mode a
// restarted pod comes back with a new IP. The first poll (prev == nil) emits
// nothing.
func Diff(prev, cur []client.StatusResult, now time.Time) []Event {
	if prev == nil {
		return nil
	}
	p, c := byID(prev), byID(cur)
	var evs []Event
	add := func(kind, node, detail string) {
		evs = append(evs, Event{At: now, Kind: kind, Node: node, Detail: detail})
	}

	if pl, cl := clusterLeader(p), clusterLeader(c); pl != cl && cl != "" {
		add("leader-change", cl, fmt.Sprintf("%s -> %s", orDash(pl), cl))
	}
	for _, id := range sortedKeys(c) {
		cs, ps, seen := c[id], p[id], p[id] != nil
		if !seen {
			add("reachable", id, fmt.Sprintf("role=%s term=%d", cs.Role, cs.Term))
			continue
		}
		if cs.Term != ps.Term {
			add("term", id, fmt.Sprintf("%d -> %d", ps.Term, cs.Term))
		}
		if cs.Role != ps.Role {
			add("role", id, fmt.Sprintf("%s -> %s", ps.Role, cs.Role))
		}
	}
	for _, id := range sortedKeys(p) {
		if c[id] == nil {
			add("unreachable", id, "")
		}
	}
	return evs
}

func byID(rs []client.StatusResult) map[string]*api.Status {
	m := map[string]*api.Status{}
	for _, r := range rs {
		if r.Status != nil {
			m[r.Status.ID] = r.Status
		}
	}
	return m
}

// clusterLeader is the self-reported leader with the highest term.
func clusterLeader(m map[string]*api.Status) string {
	best, term := "", uint64(0)
	for id, s := range m {
		if s.Role == "leader" && (best == "" || s.Term > term) {
			best, term = id, s.Term
		}
	}
	return best
}

func sortedKeys(m map[string]*api.Status) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
