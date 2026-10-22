package client

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/api"
)

// fakeNode serves /status with a fixed id and role, and answers /kv/ with put.
type fakeNode struct {
	srv  *httptest.Server
	puts atomic.Int32
}

func newFakeNode(t *testing.T, id, role string, put http.HandlerFunc) *fakeNode {
	t.Helper()
	f := &fakeNode{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(api.Status{ID: id, Role: role})
	})
	mux.HandleFunc("PUT /kv/", func(w http.ResponseWriter, r *http.Request) {
		f.puts.Add(1)
		put(w, r)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func redirectTo(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(api.HeaderLeader, id)
		w.WriteHeader(http.StatusMisdirectedRequest)
	}
}

func closedURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return "http://" + addr
}

func TestClassifyTable(t *testing.T) {
	resp := func(code int) *http.Response { return &http.Response{StatusCode: code} }
	_, dialErr := http.Get(closedURL(t))

	cases := []struct {
		name string
		resp *http.Response
		err  error
		want Outcome
	}{
		{"204", resp(204), nil, OK},
		{"200", resp(200), nil, OK},
		{"421", resp(421), nil, Failed},
		{"503", resp(503), nil, Failed},
		{"400", resp(400), nil, Failed},
		{"504", resp(504), nil, Indeterminate},
		{"500", resp(500), nil, Indeterminate},
		{"dial refused", nil, dialErr, Failed},
		{"deadline", nil, context.DeadlineExceeded, Indeterminate},
	}
	if dialErr == nil {
		t.Fatal("expected a dial error from a closed port")
	}
	for _, tc := range cases {
		if got := Classify(tc.resp, tc.err); got != tc.want {
			t.Errorf("%s: Classify = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestClassifyConnResetIsIndeterminate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			conn.Close() // request was received, then the connection dies
		}
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/kv/a", nil)
	resp, err := http.DefaultClient.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected an error from a hijacked-and-closed connection")
	}
	if got := Classify(resp, err); got != Indeterminate {
		t.Fatalf("Classify = %v, want indeterminate (request may have been applied)", got)
	}
}

func TestPutFollowsLeaderHint(t *testing.T) {
	a := newFakeNode(t, "a", "follower", redirectTo("b"))
	b := newFakeNode(t, "b", "leader", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	c := NewStatic([]string{a.srv.URL, b.srv.URL}, http.DefaultClient)
	c.leaderURL = a.srv.URL // force starting at the follower

	out, err := c.Put(context.Background(), "k", "v")
	if out != OK || err != nil {
		t.Fatalf("Put = %v,%v want ok", out, err)
	}
	if b.puts.Load() != 1 {
		t.Fatalf("leader got %d PUTs, want 1", b.puts.Load())
	}
}

func TestPutRedirectCappedAt3(t *testing.T) {
	a := newFakeNode(t, "a", "follower", redirectTo("b"))
	b := newFakeNode(t, "b", "follower", redirectTo("a"))
	c := NewStatic([]string{a.srv.URL, b.srv.URL}, http.DefaultClient)

	out, _ := c.Put(context.Background(), "k", "v")
	if out != Failed {
		t.Fatalf("Put = %v, want failed", out)
	}
	if total := a.puts.Load() + b.puts.Load(); total != 4 {
		t.Fatalf("PUT requests = %d, want 4 (initial + 3 redirects)", total)
	}
}

func TestStatusesSortedAndIncludesUnreachable(t *testing.T) {
	b := newFakeNode(t, "b", "follower", nil)
	a := newFakeNode(t, "a", "leader", nil)
	dead := closedURL(t)
	c := NewStatic([]string{dead, b.srv.URL, a.srv.URL}, &http.Client{Timeout: time.Second})

	got := c.Statuses(context.Background())
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3", len(got))
	}
	if got[0].Status == nil || got[0].Status.ID != "a" || got[1].Status == nil || got[1].Status.ID != "b" {
		t.Fatalf("order = %v, want a, b, unreachable", got)
	}
	if got[2].URL != dead || got[2].Err == nil {
		t.Fatalf("last = %+v, want unreachable %s with error", got[2], dead)
	}
}

func TestLeaderFindsLeader(t *testing.T) {
	a := newFakeNode(t, "a", "follower", nil)
	b := newFakeNode(t, "b", "leader", nil)
	c := NewStatic([]string{a.srv.URL, b.srv.URL}, http.DefaultClient)

	id, url, err := c.Leader(context.Background())
	if err != nil || id != "b" || url != b.srv.URL {
		t.Fatalf("Leader = %q %q %v, want b %s", id, url, err, b.srv.URL)
	}
}
