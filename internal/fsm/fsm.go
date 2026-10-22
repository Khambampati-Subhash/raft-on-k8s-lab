// Package fsm is the key-value state machine that Raft replicates. Raft
// decides the order of commands; this package only applies them. It has no
// network code and is deterministic: the same commands in the same order
// always produce the same map.
package fsm

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"sync"

	"github.com/hashicorp/raft"
)

const (
	OpSet    = "set"
	OpDelete = "delete"
)

// Command is one entry in the Raft log.
type Command struct {
	Op    string `json:"op"`
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

// EncodeCommand serializes c for raft.Apply.
func EncodeCommand(c Command) ([]byte, error) { return json.Marshal(c) }

// KV implements raft.FSM over an in-memory map.
type KV struct {
	mu   sync.RWMutex
	data map[string]string
}

var _ raft.FSM = (*KV)(nil)

func New() *KV { return &KV{data: map[string]string{}} }

// Apply is called by Raft once an entry is committed. It returns nil on
// success or an error for a malformed command; it never panics, because a
// panic here would crash every node that applies the same entry.
func (k *KV) Apply(l *raft.Log) any {
	var c Command
	if err := json.Unmarshal(l.Data, &c); err != nil {
		return fmt.Errorf("decode command: %w", err)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	switch c.Op {
	case OpSet:
		k.data[c.Key] = c.Value
	case OpDelete:
		delete(k.data, c.Key)
	default:
		return fmt.Errorf("unknown op %q", c.Op)
	}
	return nil
}

func (k *KV) Get(key string) (string, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	v, ok := k.data[key]
	return v, ok
}

// Snapshot copies the map so Persist can run while Apply keeps going.
func (k *KV) Snapshot() (raft.FSMSnapshot, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return &snapshot{data: maps.Clone(k.data)}, nil
}

// Restore replaces the whole state with a snapshot's contents.
func (k *KV) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	data := map[string]string{}
	if err := json.NewDecoder(rc).Decode(&data); err != nil {
		return fmt.Errorf("decode snapshot: %w", err)
	}
	k.mu.Lock()
	k.data = data
	k.mu.Unlock()
	return nil
}

type snapshot struct{ data map[string]string }

func (s *snapshot) Persist(sink raft.SnapshotSink) error {
	if err := json.NewEncoder(sink).Encode(s.data); err != nil {
		sink.Cancel()
		return fmt.Errorf("encode snapshot: %w", err)
	}
	return sink.Close()
}

func (s *snapshot) Release() {}
