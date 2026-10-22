package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func do(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestPutThenGet(t *testing.T) {
	srv := httptest.NewServer(newHandler(nil, http.DefaultClient))
	defer srv.Close()

	if code, _ := do(t, http.MethodPut, srv.URL+"/kv/a/b", "1"); code != http.StatusNoContent {
		t.Fatalf("PUT code = %d, want 204", code)
	}
	code, body := do(t, http.MethodGet, srv.URL+"/kv/a/b", "")
	if code != http.StatusOK || body != "1" {
		t.Fatalf("GET = %d %q, want 200 \"1\"", code, body)
	}
}

func TestGetMissingIs404(t *testing.T) {
	srv := httptest.NewServer(newHandler(nil, http.DefaultClient))
	defer srv.Close()

	if code, _ := do(t, http.MethodGet, srv.URL+"/kv/nope", ""); code != http.StatusNotFound {
		t.Fatalf("GET code = %d, want 404", code)
	}
}

func TestReplicaWriteDoesNotFanOut(t *testing.T) {
	var hits atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer peer.Close()
	srv := httptest.NewServer(newHandler([]string{peer.URL}, http.DefaultClient))
	defer srv.Close()

	do(t, http.MethodPut, srv.URL+"/kv/k?replica=1", "v")
	time.Sleep(200 * time.Millisecond)
	if n := hits.Load(); n != 0 {
		t.Fatalf("replica write fanned out %d times, want 0", n)
	}

	do(t, http.MethodPut, srv.URL+"/kv/k", "v")
	deadline := time.Now().Add(time.Second)
	for hits.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("client write fanned out %d times, want 1", n)
	}
}
