package loadgen

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/api"
	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/client"
)

type mapReader struct {
	mu    sync.Mutex
	data  map[string]string
	calls int
	fail  int // fail the first N calls
}

func (m *mapReader) Get(_ context.Context, k string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls <= m.fail {
		return "", false, errors.New("no leader")
	}
	v, ok := m.data[k]
	return v, ok, nil
}

var t0 = time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)

func rec(key, val string, o client.Outcome, endMs int) Record {
	return Record{Key: key, Value: val, Outcome: o, Start: t0, End: t0.Add(time.Duration(endMs) * time.Millisecond)}
}

func TestVerifyCountsLostAck(t *testing.T) {
	r := &mapReader{data: map[string]string{"a": "1"}}
	rep := Verify(context.Background(), []Record{rec("a", "1", client.OK, 1), rec("b", "2", client.OK, 2)}, r, time.Second)
	if rep.Lost != 1 || len(rep.LostKeys) != 1 || rep.LostKeys[0] != "b" || rep.OK != 2 {
		t.Fatalf("report = %+v, want Lost=1 [b]", rep)
	}
}

func TestVerifyWrongValueIsLost(t *testing.T) {
	r := &mapReader{data: map[string]string{"a": "other"}}
	if rep := Verify(context.Background(), []Record{rec("a", "1", client.OK, 1)}, r, time.Second); rep.Lost != 1 {
		t.Fatalf("Lost = %d, want 1", rep.Lost)
	}
}

func TestVerifyIndeterminateNotLost(t *testing.T) {
	r := &mapReader{data: map[string]string{"a": "1"}}
	recs := []Record{rec("a", "1", client.Indeterminate, 1), rec("b", "2", client.Indeterminate, 2)}
	rep := Verify(context.Background(), recs, r, time.Second)
	if rep.Lost != 0 || rep.IndeterminatePresent != 1 || rep.IndeterminateAbsent != 1 || rep.Indeterminate != 2 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyFailedNotChecked(t *testing.T) {
	r := &mapReader{data: map[string]string{}}
	rep := Verify(context.Background(), []Record{rec("a", "1", client.Failed, 1)}, r, time.Second)
	if r.calls != 0 || rep.Failed != 1 || rep.Lost != 0 {
		t.Fatalf("calls=%d report=%+v", r.calls, rep)
	}
}

func TestVerifyRetriesUntilLeader(t *testing.T) {
	r := &mapReader{data: map[string]string{"a": "1"}, fail: 3}
	rep := Verify(context.Background(), []Record{rec("a", "1", client.OK, 1)}, r, 5*time.Second)
	if rep.Lost != 0 || rep.VerifyErrors != 0 {
		t.Fatalf("report = %+v, want verified after retries", rep)
	}
}

func TestVerifyUnreachableCountsVerifyErrorNotLost(t *testing.T) {
	r := &mapReader{fail: 1 << 30}
	rep := Verify(context.Background(), []Record{rec("a", "1", client.OK, 1)}, r, 50*time.Millisecond)
	if rep.Lost != 0 || rep.VerifyErrors != 1 {
		t.Fatalf("report = %+v, want VerifyErrors=1 Lost=0", rep)
	}
}

func TestGapsDetectsWindow(t *testing.T) {
	recs := []Record{rec("a", "", client.OK, 0), rec("b", "", client.OK, 100), rec("x", "", client.Failed, 500),
		rec("c", "", client.OK, 900), rec("d", "", client.OK, 1000)}
	g := Gaps(recs, 500*time.Millisecond)
	if len(g) != 1 || !g[0].From.Equal(t0.Add(100*time.Millisecond)) || !g[0].To.Equal(t0.Add(900*time.Millisecond)) {
		t.Fatalf("gaps = %+v, want one 100ms-900ms", g)
	}
}

func TestPercentiles(t *testing.T) {
	var recs []Record
	for i := 1; i <= 100; i++ {
		recs = append(recs, Record{Key: strconv.Itoa(i), Outcome: client.OK, Start: t0, End: t0.Add(time.Duration(i) * time.Millisecond)})
	}
	rep := Verify(context.Background(), nil, &mapReader{}, time.Second)
	rep.AddLatency(recs)
	if rep.P50 != 50*time.Millisecond || rep.P99 != 99*time.Millisecond {
		t.Fatalf("P50=%v P99=%v, want 50ms 99ms", rep.P50, rep.P99)
	}
}

type countingWriter struct {
	mu   sync.Mutex
	keys map[string]int
}

func (w *countingWriter) Put(_ context.Context, k, _ string) (client.Outcome, error) {
	w.mu.Lock()
	w.keys[k]++
	w.mu.Unlock()
	time.Sleep(time.Millisecond)
	return client.OK, nil
}

func TestRunKeysUnique(t *testing.T) {
	w := &countingWriter{keys: map[string]int{}}
	recs := Run(context.Background(), w, 4, 50*time.Millisecond, "run1")
	if len(recs) == 0 {
		t.Fatal("no writes")
	}
	for k, n := range w.keys {
		if n != 1 || !strings.HasPrefix(k, "run1/") {
			t.Fatalf("key %q written %d times", k, n)
		}
	}
	if len(recs) != len(w.keys) {
		t.Fatalf("records %d != distinct keys %d", len(recs), len(w.keys))
	}
}

type fakeStale struct {
	values map[string]string // url -> counter value it returns
}

func (f *fakeStale) Statuses(context.Context) []client.StatusResult {
	var out []client.StatusResult
	for u := range f.values {
		out = append(out, client.StatusResult{URL: u, Status: &api.Status{ID: strings.TrimPrefix(u, "http://")}})
	}
	return out
}

func (f *fakeStale) GetStale(_ context.Context, u, _ string) (string, bool, error) {
	return f.values[u], true, nil
}

func TestProbeReportsLag(t *testing.T) {
	w := &countingWriter{keys: map[string]int{}}
	r := &fakeStale{values: map[string]string{"http://a": "1", "http://b": "0"}}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	lag := Probe(ctx, w, r, "run1/probe", 20*time.Millisecond, 50*time.Millisecond)
	if lag["a"] == 0 || lag["b"] <= lag["a"] {
		t.Fatalf("lag = %v, want a>0 and b>a", lag)
	}
}
