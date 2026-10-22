package node

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

// Deps are the pieces Raft needs to persist and talk. Tests pass in-memory
// versions; kvnode passes disk-backed ones from NewDiskDeps.
type Deps struct {
	Logs      raft.LogStore
	Stable    raft.StableStore
	Snaps     raft.SnapshotStore
	Transport raft.Transport
}

// NewDiskDeps opens dataDir/raft.db (log + stable store), dataDir/snapshots
// (keeps the 2 newest) and a DNS-advertising TCP transport.
// The returned Closer closes the transport and the bolt store.
func NewDiskDeps(dataDir, bind, advertise string, logOut io.Writer) (Deps, io.Closer, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return Deps{}, nil, fmt.Errorf("create data dir: %w", err)
	}
	bolt, err := raftboltdb.NewBoltStore(filepath.Join(dataDir, "raft.db"))
	if err != nil {
		return Deps{}, nil, fmt.Errorf("open bolt store: %w", err)
	}
	snaps, err := raft.NewFileSnapshotStore(filepath.Join(dataDir, "snapshots"), 2, logOut)
	if err != nil {
		bolt.Close()
		return Deps{}, nil, fmt.Errorf("open snapshot store: %w", err)
	}
	tr, err := NewDNSTransport(bind, advertise, logOut)
	if err != nil {
		bolt.Close()
		return Deps{}, nil, err
	}
	d := Deps{Logs: bolt, Stable: bolt, Snaps: snaps, Transport: tr}
	return d, closers{tr, bolt}, nil
}

type closers []io.Closer

func (cs closers) Close() error {
	var first error
	for _, c := range cs {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
