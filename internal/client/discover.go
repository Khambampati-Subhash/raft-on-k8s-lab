package client

import (
	"errors"
	"net/http"
	"strings"
)

// Discover builds a Cluster from the --nodes / --service + --port flags that
// loadgen, raftwatch and raftctl share. Exactly one of nodes or service is set.
func Discover(nodes, service string, port int, hc *http.Client) (*Cluster, error) {
	switch {
	case nodes != "" && service != "":
		return nil, errors.New("use either --nodes or --service, not both")
	case nodes != "":
		return NewStatic(strings.Split(nodes, ","), hc), nil
	case service != "":
		return NewDNS(service, port, hc), nil
	default:
		return nil, errors.New("one of --nodes or --service is required")
	}
}
