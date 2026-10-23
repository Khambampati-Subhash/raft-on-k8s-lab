// Command loadgen writes unique keys to the cluster for a while, then reads
// every acknowledged key back from the leader. Any acknowledged write that is
// missing or wrong is "lost" and makes the process exit 1.
//
// Exit codes: 0 clean, 1 lost > 0, 2 verify could not reach a leader.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/client"
	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/loadgen"
)

func main() {
	nodes := flag.String("nodes", "", "comma-separated node URLs (static mode)")
	service := flag.String("service", "", "DNS name to resolve for node IPs (headless Service)")
	port := flag.Int("port", 8080, "HTTP port with --service")
	workers := flag.Int("workers", 4, "concurrent writers")
	duration := flag.Duration("duration", 60*time.Second, "length of the write phase")
	runID := flag.String("run-id", time.Now().UTC().Format("20060102T150405"), "key prefix for this run")
	reportPath := flag.String("report", "/tmp/report.json", "where to write the JSON report (empty = skip)")
	staleProbe := flag.Bool("stale-probe", false, "also measure stale-read lag on every node")
	gap := flag.Duration("gap", 500*time.Millisecond, "report windows longer than this with no ok write")
	verifyWait := flag.Duration("verify-wait", 60*time.Second, "how long verify retries a read before giving up")
	flag.Parse()
	log.SetFlags(0)

	c, err := client.Discover(*nodes, *service, *port, &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var lag map[string]uint64
	var wg sync.WaitGroup
	if *staleProbe {
		probeCtx, cancel := context.WithTimeout(ctx, *duration)
		defer cancel()
		wg.Add(1)
		go func() {
			defer wg.Done()
			lag = loadgen.Probe(probeCtx, c, c, *runID+"/probe", 100*time.Millisecond, 500*time.Millisecond)
		}()
	}
	log.Printf("loadgen: run %s, %d workers for %v", *runID, *workers, *duration)
	recs := loadgen.Run(ctx, c, *workers, *duration, *runID)
	wg.Wait()

	log.Printf("loadgen: verifying %d writes", len(recs))
	rep := loadgen.Verify(context.Background(), recs, c, *verifyWait)
	rep.RunID, rep.StaleLag = *runID, lag
	rep.Gaps = loadgen.Gaps(recs, *gap)
	rep.AddLatency(recs)

	fmt.Print(rep.Summary())
	out, _ := json.MarshalIndent(rep.JSON(), "", "  ")
	fmt.Println(string(out))
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, append(out, '\n'), 0o644); err != nil {
			log.Printf("loadgen: write report: %v", err)
		}
	}
	switch {
	case rep.Lost > 0:
		os.Exit(1)
	case rep.VerifyErrors > 0:
		os.Exit(2)
	}
}
