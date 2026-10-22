package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/api"
	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/node"
)

type fakeStore struct {
	err      error
	value    string
	found    bool
	leaderID string

	gotKey   string
	gotStale bool
	gotJoin  api.JoinRequest
}

func (f *fakeStore) Set(_ context.Context, k, _ string) error { f.gotKey = k; return f.err }
func (f *fakeStore) Delete(_ context.Context, k string) error { f.gotKey = k; return f.err }
func (f *fakeStore) Get(_ context.Context, k string, stale bool) (string, bool, error) {
	f.gotKey, f.gotStale = k, stale
	return f.value, f.found, f.err
}
func (f *fakeStore) Join(id, addr string) error {
	f.gotJoin = api.JoinRequest{ID: id, Addr: addr}
	return f.err
}
func (f *fakeStore) Leave(id string) error { return f.err }
func (f *fakeStore) Status() api.Status    { return api.Status{ID: "kv-0", LeaderID: f.leaderID} }

func call(t *testing.T, s Store, method, path, body string) *http.Response {
	t.Helper()
	srv := httptest.NewServer(New(s))
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func readBody(resp *http.Response) string { b, _ := io.ReadAll(resp.Body); return string(b) }

func TestStatusCodes(t *testing.T) {
	notLeader := &node.NotLeaderError{LeaderID: "kv-1"}
	cases := []struct {
		name         string
		method, path string
		body         string
		store        fakeStore
		wantCode     int
		wantLeader   string
		wantBody     string
	}{
		{"put ok", "PUT", "/kv/a", "v", fakeStore{}, 204, "", ""},
		{"put not leader", "PUT", "/kv/a", "v", fakeStore{err: notLeader}, 421, "kv-1", ""},
		{"put no leader", "PUT", "/kv/a", "v", fakeStore{err: node.ErrNoLeader}, 503, "", ""},
		{"put timeout", "PUT", "/kv/a", "v", fakeStore{err: node.ErrTimeout}, 504, "", "outcome-unknown"},
		{"put other err", "PUT", "/kv/a", "v", fakeStore{err: errors.New("boom")}, 500, "", ""},

		{"delete ok", "DELETE", "/kv/a", "", fakeStore{}, 204, "", ""},
		{"delete not leader", "DELETE", "/kv/a", "", fakeStore{err: notLeader}, 421, "kv-1", ""},
		{"delete no leader", "DELETE", "/kv/a", "", fakeStore{err: node.ErrNoLeader}, 503, "", ""},
		{"delete timeout", "DELETE", "/kv/a", "", fakeStore{err: node.ErrTimeout}, 504, "", "outcome-unknown"},

		{"get found", "GET", "/kv/a", "", fakeStore{value: "v", found: true}, 200, "", "v"},
		{"get missing", "GET", "/kv/a", "", fakeStore{}, 404, "", ""},
		{"get not leader", "GET", "/kv/a", "", fakeStore{err: notLeader}, 421, "kv-1", ""},
		{"get no leader", "GET", "/kv/a", "", fakeStore{err: node.ErrNoLeader}, 503, "", ""},

		{"join ok", "POST", "/join", `{"id":"kv-3","addr":"kv-3:7000"}`, fakeStore{}, 204, "", ""},
		{"join bad json", "POST", "/join", `{`, fakeStore{}, 400, "", ""},
		{"join missing addr", "POST", "/join", `{"id":"kv-3"}`, fakeStore{}, 400, "", ""},
		{"join not leader", "POST", "/join", `{"id":"kv-3","addr":"a"}`, fakeStore{err: notLeader}, 421, "kv-1", ""},
		{"leave ok", "POST", "/leave", `{"id":"kv-3"}`, fakeStore{}, 204, "", ""},
		{"leave missing id", "POST", "/leave", `{}`, fakeStore{}, 400, "", ""},
		{"leave not leader", "POST", "/leave", `{"id":"kv-3"}`, fakeStore{err: notLeader}, 421, "kv-1", ""},

		{"readyz no leader", "GET", "/readyz", "", fakeStore{}, 503, "", ""},
		{"readyz leader known", "GET", "/readyz", "", fakeStore{leaderID: "kv-0"}, 200, "", ""},
		{"healthz", "GET", "/healthz", "", fakeStore{}, 200, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.store
			resp := call(t, &s, tc.method, tc.path, tc.body)
			if resp.StatusCode != tc.wantCode {
				t.Fatalf("code = %d, want %d", resp.StatusCode, tc.wantCode)
			}
			if got := resp.Header.Get(api.HeaderLeader); got != tc.wantLeader {
				t.Fatalf("%s = %q, want %q", api.HeaderLeader, got, tc.wantLeader)
			}
			if tc.wantBody != "" {
				if got := strings.TrimSpace(readBody(resp)); got != tc.wantBody {
					t.Fatalf("body = %q, want %q", got, tc.wantBody)
				}
			}
		})
	}
}

func TestStaleQueryPassedToStore(t *testing.T) {
	s := &fakeStore{found: true}
	call(t, s, "GET", "/kv/a?stale=true", "")
	if !s.gotStale {
		t.Fatal("stale=true not passed to store")
	}
	call(t, s, "GET", "/kv/a", "")
	if s.gotStale {
		t.Fatal("default read must be linearizable (stale=false)")
	}
}

func TestKeyWithSlashes(t *testing.T) {
	s := &fakeStore{}
	resp := call(t, s, "PUT", "/kv/run1/0/42", "v")
	if resp.StatusCode != 204 || s.gotKey != "run1/0/42" {
		t.Fatalf("code=%d key=%q, want 204 and key run1/0/42", resp.StatusCode, s.gotKey)
	}
}

func TestStatusJSON(t *testing.T) {
	resp := call(t, &fakeStore{leaderID: "kv-0"}, "GET", "/status", "")
	var st api.Status
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil || st.ID != "kv-0" || st.LeaderID != "kv-0" {
		t.Fatalf("status = %+v, err = %v", st, err)
	}
}

func TestBodyTooLarge(t *testing.T) {
	resp := call(t, &fakeStore{}, "PUT", "/kv/a", strings.Repeat("x", 1<<20+1))
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413", resp.StatusCode)
	}
}
