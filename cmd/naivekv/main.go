// Command naivekv is the deliberately naive key-value server used in journey
// stages 0 and 1. It keeps data in memory and, when given peers, copies each
// write to them on a best-effort basis: no ordering, no acknowledgement, no
// catch-up. It exists to be broken.
package main

import (
	"flag"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
)

type server struct {
	mu    sync.RWMutex
	data  map[string]string
	peers []string
	hc    *http.Client
}

func newHandler(peers []string, hc *http.Client) http.Handler {
	s := &server{data: map[string]string{}, peers: peers, hc: hc}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /kv/{key...}", s.put)
	mux.HandleFunc("GET /kv/{key...}", s.get)
	return mux
}

func (s *server) put(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	value := string(body)

	s.mu.Lock()
	s.data[key] = value
	s.mu.Unlock()

	if r.URL.Query().Get("replica") != "1" {
		for _, p := range s.peers {
			go s.replicate(p, key, value)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// replicate fires and forgets. Errors are logged and dropped: that is the bug.
func (s *server) replicate(peer, key, value string) {
	req, err := http.NewRequest(http.MethodPut, peer+"/kv/"+key+"?replica=1", strings.NewReader(value))
	if err != nil {
		log.Printf("replicate to %s: %v", peer, err)
		return
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		log.Printf("replicate to %s: %v (dropped)", peer, err)
		return
	}
	resp.Body.Close()
}

func (s *server) get(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	v, ok := s.data[r.PathValue("key")]
	s.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	io.WriteString(w, v)
}

func main() {
	addr := flag.String("http", ":8080", "HTTP listen address")
	peers := flag.String("peers", "", "comma-separated peer base URLs to copy writes to")
	flag.Parse()

	var ps []string
	if *peers != "" {
		ps = strings.Split(*peers, ",")
	}
	log.Printf("naivekv listening on %s, peers=%v", *addr, ps)
	log.Fatal(http.ListenAndServe(*addr, newHandler(ps, http.DefaultClient)))
}
