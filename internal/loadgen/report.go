package loadgen

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/client"
)

// Window is a stretch of time with no successful write.
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Report is the verdict of one loadgen run.
type Report struct {
	RunID                                     string
	Total, OK, Failed, Indeterminate, Lost    int
	IndeterminatePresent, IndeterminateAbsent int
	VerifyErrors                              int // reads that never succeeded; not Lost
	LostKeys                                  []string
	P50, P99                                  time.Duration
	Gaps                                      []Window
	StaleLag                                  map[string]uint64 // node ID → max observed lag; nil if probe off
}

// JSON is the report as written to the --report file (durations in ms).
func (r Report) JSON() map[string]any {
	return map[string]any{
		"run_id": r.RunID, "total": r.Total, "ok": r.OK, "failed": r.Failed,
		"indeterminate": r.Indeterminate, "indeterminate_present": r.IndeterminatePresent,
		"indeterminate_absent": r.IndeterminateAbsent, "lost": r.Lost, "lost_keys": r.LostKeys,
		"verify_errors": r.VerifyErrors, "p50_ms": ms(r.P50), "p99_ms": ms(r.P99),
		"gaps": r.Gaps, "stale_lag": r.StaleLag,
	}
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// Gaps returns the windows between consecutive ok completions longer than threshold.
func Gaps(recs []Record, threshold time.Duration) []Window {
	var ends []time.Time
	for _, r := range recs {
		if r.Outcome == client.OK {
			ends = append(ends, r.End)
		}
	}
	slices.SortFunc(ends, func(a, b time.Time) int { return a.Compare(b) })
	var out []Window
	for i := 1; i < len(ends); i++ {
		if ends[i].Sub(ends[i-1]) > threshold {
			out = append(out, Window{From: ends[i-1], To: ends[i]})
		}
	}
	return out
}

// AddLatency sets P50/P99 (nearest rank) over the ok writes in recs.
func (r *Report) AddLatency(recs []Record) {
	var lat []time.Duration
	for _, rec := range recs {
		if rec.Outcome == client.OK {
			lat = append(lat, rec.End.Sub(rec.Start))
		}
	}
	if len(lat) == 0 {
		return
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	rank := func(p float64) time.Duration { return lat[int(math.Ceil(p*float64(len(lat))))-1] }
	r.P50, r.P99 = rank(0.50), rank(0.99)
}

// Summary is the human-readable verdict.
func (r Report) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "run %s\n", r.RunID)
	fmt.Fprintf(&b, "  writes: %d total, %d ok, %d failed, %d indeterminate (%d present, %d absent)\n",
		r.Total, r.OK, r.Failed, r.Indeterminate, r.IndeterminatePresent, r.IndeterminateAbsent)
	fmt.Fprintf(&b, "  latency (ok): p50 %v, p99 %v\n", r.P50.Round(time.Microsecond), r.P99.Round(time.Microsecond))
	fmt.Fprintf(&b, "  unavailability windows: %d\n", len(r.Gaps))
	for _, g := range r.Gaps {
		fmt.Fprintf(&b, "    %s .. %s (%v)\n", g.From.Format("15:04:05.000"), g.To.Format("15:04:05.000"), g.To.Sub(g.From).Round(time.Millisecond))
	}
	if r.StaleLag != nil {
		ids := make([]string, 0, len(r.StaleLag))
		for id := range r.StaleLag {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		fmt.Fprintf(&b, "  stale-read max lag:")
		for _, id := range ids {
			fmt.Fprintf(&b, " %s=%d", id, r.StaleLag[id])
		}
		fmt.Fprintln(&b)
	}
	if r.VerifyErrors > 0 {
		fmt.Fprintf(&b, "  verify errors: %d (could not read back; not counted as lost)\n", r.VerifyErrors)
	}
	fmt.Fprintf(&b, "  Lost: %d\n", r.Lost)
	for _, k := range r.LostKeys {
		fmt.Fprintf(&b, "    %s\n", k)
	}
	return b.String()
}
