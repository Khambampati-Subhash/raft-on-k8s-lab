// Command raftwatch polls every node's /status and shows a live table plus a
// stream of events: leader changes, term bumps, nodes disappearing.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/client"
	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/watch"
)

func main() {
	nodes := flag.String("nodes", "", "comma-separated node URLs (static mode)")
	service := flag.String("service", "", "DNS name to resolve for node IPs (headless Service)")
	port := flag.Int("port", 8080, "HTTP port with --service")
	interval := flag.Duration("interval", 250*time.Millisecond, "poll interval")
	eventsOnly := flag.Bool("events-only", false, "print only event lines, no table")
	forDur := flag.Duration("for", 0, "stop after this long (0 = run until interrupted)")
	flag.Parse()

	c, err := client.Discover(*nodes, *service, *port, &http.Client{Timeout: *interval * 2})
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *forDur > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *forDur)
		defer cancel()
	}

	var prev []client.StatusResult
	var recent []watch.Event
	tick := time.NewTicker(*interval)
	defer tick.Stop()
	for {
		pollCtx, cancel := context.WithTimeout(ctx, *interval*2)
		cur := c.Statuses(pollCtx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		evs := watch.Diff(prev, cur, time.Now())
		prev = cur
		if *eventsOnly {
			for _, e := range evs {
				fmt.Println(e)
			}
		} else {
			recent = append(recent, evs...)
			if len(recent) > 15 {
				recent = recent[len(recent)-15:]
			}
			fmt.Print("\033[H\033[2J")
			fmt.Println(time.Now().Format("15:04:05.000"))
			watch.Render(os.Stdout, cur)
			fmt.Println("\nrecent events:")
			for _, e := range recent {
				fmt.Println(" ", e)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
