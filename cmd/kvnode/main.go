// Command kvnode is one member of the Raft key-value cluster.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/api"
	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/client"
	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/httpapi"
	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/node"
)

func main() {
	var (
		id         = flag.String("id", os.Getenv("POD_NAME"), "server ID (default $POD_NAME)")
		httpAddr   = flag.String("http", ":8080", "HTTP listen address")
		raftBind   = flag.String("raft-bind", ":7000", "Raft listen address")
		advertise  = flag.String("raft-advertise", "", "Raft address other nodes use to reach this one (host:port; may be a DNS name)")
		dataDir    = flag.String("data", "/data", "directory for the Raft log, stable store and snapshots")
		bootMode   = flag.String("bootstrap", "auto", "auto | true | false (auto: only an ID ending in -0 bootstraps)")
		join       = flag.String("join", "", "comma-separated HTTP base URLs to ask for membership")
		heartbeat  = flag.Duration("heartbeat-timeout", 0, "Raft heartbeat timeout (0 = library default 1s)")
		election   = flag.Duration("election-timeout", 0, "Raft election timeout (0 = library default 1s)")
		lease      = flag.Duration("leader-lease-timeout", 0, "Raft leader lease timeout (0 = library default 500ms)")
		applyTO    = flag.Duration("apply-timeout", 5*time.Second, "max time to wait for a write to commit")
		snapThresh = flag.Uint64("snapshot-threshold", 1024, "log entries between snapshots")
		debugEP    = flag.Bool("debug-endpoints", false, "wrap the log store in a fault injector and expose POST /debug/store-fault")
	)
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	if *id == "" || *advertise == "" {
		log.Fatal("--id (or $POD_NAME) and --raft-advertise are required")
	}
	bootstrap, err := resolveBootstrap(*bootMode, *id)
	if err != nil {
		log.Fatal(err)
	}

	deps, closer, err := node.NewDiskDeps(*dataDir, *raftBind, *advertise, os.Stderr)
	if err != nil {
		log.Fatal(err)
	}
	var opts []httpapi.Option
	if *debugEP {
		fs := node.NewFaultLogStore(deps.Logs)
		deps.Logs = fs
		opts = append(opts, httpapi.WithFaultInjector(fs))
	}
	n, err := node.New(node.Config{
		ID:                 *id,
		Bootstrap:          bootstrap,
		HeartbeatTimeout:   *heartbeat,
		ElectionTimeout:    *election,
		LeaderLeaseTimeout: *lease,
		ApplyTimeout:       *applyTO,
		SnapshotThreshold:  *snapThresh,
	}, deps)
	if err != nil {
		closer.Close()
		log.Fatal(err)
	}

	srv := &http.Server{Addr: *httpAddr, Handler: httpapi.New(n, opts...)}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http: %v", err)
		}
	}()
	log.Printf("kvnode %s: http %s, raft %s (advertise %s), bootstrap=%v", *id, *httpAddr, *raftBind, *advertise, bootstrap)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if !bootstrap && *join != "" {
		peers := client.NewStatic(strings.Split(*join, ","), &http.Client{Timeout: 3 * time.Second})
		isMember := func() bool {
			return slices.ContainsFunc(n.Status().Servers, func(s api.Server) bool { return s.ID == *id })
		}
		go joinLoop(ctx, peers, *id, *advertise, isMember, Backoff{Initial: 200 * time.Millisecond, Max: 5 * time.Second})
	}

	<-ctx.Done()
	log.Printf("kvnode %s: shutting down", *id)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	if err := n.Shutdown(); err != nil {
		log.Printf("raft shutdown: %v", err)
	}
	closer.Close()
}
