// Command raftctl is a tiny admin CLI for the cluster:
//
//	raftctl [discovery flags] leader       print the leader's ID
//	raftctl [discovery flags] status       print every node's /status as JSON
//	raftctl [discovery flags] leave <id>   remove a member (via the leader)
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/client"
)

func main() {
	nodes := flag.String("nodes", "", "comma-separated node URLs (static mode)")
	service := flag.String("service", "", "DNS name to resolve for node IPs (headless Service)")
	port := flag.Int("port", 8080, "HTTP port with --service")
	timeout := flag.Duration("timeout", 10*time.Second, "overall timeout")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: raftctl [--nodes URLS | --service NAME --port N] leader | status | leave <id>")
		flag.PrintDefaults()
	}
	flag.Parse()
	log.SetFlags(0)

	c, err := client.Discover(*nodes, *service, *port, &http.Client{Timeout: 3 * time.Second})
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	switch args := flag.Args(); {
	case len(args) == 1 && args[0] == "leader":
		id, _, err := c.Leader(ctx)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(id)
	case len(args) == 1 && args[0] == "status":
		type row struct {
			URL    string `json:"url"`
			Status any    `json:"status,omitempty"`
			Error  string `json:"error,omitempty"`
		}
		var out []row
		for _, r := range c.Statuses(ctx) {
			x := row{URL: r.URL, Status: r.Status}
			if r.Err != nil {
				x.Error = r.Err.Error()
			}
			out = append(out, x)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(out)
	case len(args) == 2 && args[0] == "leave":
		if err := c.Leave(ctx, args[1]); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s removed\n", args[1])
	default:
		flag.Usage()
		os.Exit(2)
	}
}
