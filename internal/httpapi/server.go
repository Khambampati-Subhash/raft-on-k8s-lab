// Package httpapi exposes a Store over HTTP. It knows nothing about Raft; it
// only translates the Store's three kinds of failure into status codes:
//
//	NotLeaderError → 421 + X-Raft-Leader: <id>  (definitely not applied; ask the leader)
//	ErrNoLeader    → 503                        (definitely not applied; try later)
//	ErrTimeout     → 504 "outcome-unknown"      (may or may not have been applied)
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/api"
	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/node"
)

const maxBody = 1 << 20

// Store is everything the HTTP layer needs from a node.
type Store interface {
	Set(ctx context.Context, key, value string) error
	Delete(ctx context.Context, key string) error
	Get(ctx context.Context, key string, stale bool) (string, bool, error)
	Join(id, addr string) error
	Leave(id string) error
	Status() api.Status
}

// Faulter injects storage faults (node.FaultLogStore). Debug only.
type Faulter interface {
	SetFault(delay time.Duration, failRate float64)
}

// Option configures optional, non-default endpoints.
type Option func(*http.ServeMux)

// WithFaultInjector mounts POST /debug/store-fault {"delay":"200ms","failRate":0.5}.
// Zero values turn the fault off. Without this option the route doesn't exist.
func WithFaultInjector(f Faulter) Option {
	return func(mux *http.ServeMux) {
		mux.HandleFunc("POST /debug/store-fault", func(w http.ResponseWriter, r *http.Request) {
			var req api.StoreFault
			if !decode(w, r, &req) {
				return
			}
			var d time.Duration
			if req.Delay != "" {
				var err error
				if d, err = time.ParseDuration(req.Delay); err != nil || d < 0 {
					http.Error(w, "delay: want a duration like 200ms", http.StatusBadRequest)
					return
				}
			}
			if req.FailRate < 0 || req.FailRate > 1 {
				http.Error(w, "failRate: want 0..1", http.StatusBadRequest)
				return
			}
			f.SetFault(d, req.FailRate)
			w.WriteHeader(http.StatusNoContent)
		})
	}
}

type handler struct{ s Store }

func New(s Store, opts ...Option) http.Handler {
	h := handler{s}
	mux := http.NewServeMux()
	for _, o := range opts {
		o(mux)
	}
	mux.HandleFunc("PUT /kv/{key...}", h.put)
	mux.HandleFunc("DELETE /kv/{key...}", h.delete)
	mux.HandleFunc("GET /kv/{key...}", h.get)
	mux.HandleFunc("GET /status", h.status)
	mux.HandleFunc("POST /join", h.join)
	mux.HandleFunc("POST /leave", h.leave)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", h.ready)
	return mux
}

func (h handler) put(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "value too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeResult(w, h.s.Set(r.Context(), r.PathValue("key"), string(body)))
}

func (h handler) delete(w http.ResponseWriter, r *http.Request) {
	writeResult(w, h.s.Delete(r.Context(), r.PathValue("key")))
}

func (h handler) get(w http.ResponseWriter, r *http.Request) {
	v, ok, err := h.s.Get(r.Context(), r.PathValue("key"), r.URL.Query().Get("stale") == "true")
	if err != nil {
		writeError(w, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	io.WriteString(w, v)
}

func (h handler) status(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.s.Status())
}

func (h handler) join(w http.ResponseWriter, r *http.Request) {
	var req api.JoinRequest
	if !decode(w, r, &req) {
		return
	}
	if req.ID == "" || req.Addr == "" {
		http.Error(w, "id and addr are required", http.StatusBadRequest)
		return
	}
	writeResult(w, h.s.Join(req.ID, req.Addr))
}

func (h handler) leave(w http.ResponseWriter, r *http.Request) {
	var req api.LeaveRequest
	if !decode(w, r, &req) {
		return
	}
	if req.ID == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	writeResult(w, h.s.Leave(req.ID))
}

// ready reports whether this node can route a request: it knows a leader.
func (h handler) ready(w http.ResponseWriter, _ *http.Request) {
	if h.s.Status().LeaderID == "" {
		http.Error(w, "no leader known", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeResult(w http.ResponseWriter, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeError(w http.ResponseWriter, err error) {
	var nle *node.NotLeaderError
	switch {
	case errors.As(err, &nle):
		w.Header().Set(api.HeaderLeader, nle.LeaderID)
		http.Error(w, err.Error(), http.StatusMisdirectedRequest)
	case errors.Is(err, node.ErrNoLeader):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	case errors.Is(err, node.ErrTimeout):
		http.Error(w, "outcome-unknown", http.StatusGatewayTimeout)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
