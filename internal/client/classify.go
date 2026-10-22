package client

import (
	"errors"
	"net"
	"net/http"
)

// Outcome is what a client can honestly say about a write.
type Outcome int

const (
	// OK: the cluster committed the write.
	OK Outcome = iota
	// Failed: the write was definitely not applied.
	Failed
	// Indeterminate: the write may or may not have been applied.
	Indeterminate
)

func (o Outcome) String() string {
	switch o {
	case OK:
		return "ok"
	case Failed:
		return "failed"
	default:
		return "indeterminate"
	}
}

// Classify decides an Outcome from one HTTP attempt.
//
// The rule that matters: an error is only "failed" if the request provably
// never left this machine (the TCP dial itself failed). Any error after the
// connection was made — a reset, a timeout — means the server may have
// received and applied the request.
func Classify(resp *http.Response, err error) Outcome {
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return Failed
		}
		return Indeterminate
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return OK
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return Failed // 421 not leader, 400 bad request, 413 too large
	case resp.StatusCode == http.StatusServiceUnavailable:
		return Failed // no leader: rejected before entering the log
	default:
		return Indeterminate // 504 outcome-unknown and any other 5xx
	}
}
