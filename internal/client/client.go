// Package client is a small HTTP client for a kvnode cluster. It discovers
// nodes, finds and remembers the leader, and follows 421 redirects.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/api"
)

const maxRedirects = 3

// StatusResult is one node's /status, or the error reaching it.
type StatusResult struct {
	URL    string
	Status *api.Status
	Err    error
}

// Cluster talks to a set of kvnodes found either from a static URL list or by
// resolving a (headless Service) DNS name on every refresh.
type Cluster struct {
	hc        *http.Client
	static    []string
	dnsHost   string
	dnsPort   int
	mu        sync.Mutex
	leaderURL string
	idToURL   map[string]string
}

func NewStatic(urls []string, hc *http.Client) *Cluster {
	return &Cluster{hc: hc, static: urls, idToURL: map[string]string{}}
}

func NewDNS(host string, port int, hc *http.Client) *Cluster {
	return &Cluster{hc: hc, dnsHost: host, dnsPort: port, idToURL: map[string]string{}}
}

func (c *Cluster) endpoints(ctx context.Context) ([]string, error) {
	if c.dnsHost == "" {
		return c.static, nil
	}
	ips, err := net.DefaultResolver.LookupHost(ctx, c.dnsHost)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", c.dnsHost, err)
	}
	out := make([]string, len(ips))
	for i, ip := range ips {
		out[i] = "http://" + net.JoinHostPort(ip, strconv.Itoa(c.dnsPort))
	}
	return out, nil
}

// Statuses asks every node for /status in parallel. Results are sorted by
// node ID, with unreachable nodes last (sorted by URL).
func (c *Cluster) Statuses(ctx context.Context) []StatusResult {
	eps, err := c.endpoints(ctx)
	if err != nil {
		return []StatusResult{{URL: c.dnsHost, Err: err}}
	}
	out := make([]StatusResult, len(eps))
	var wg sync.WaitGroup
	for i, ep := range eps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = StatusResult{URL: ep}
			out[i].Status, out[i].Err = c.status(ctx, ep)
		}()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Status == nil) != (b.Status == nil) {
			return a.Status != nil
		}
		if a.Status != nil {
			return a.Status.ID < b.Status.ID
		}
		return a.URL < b.URL
	})
	c.mu.Lock()
	for _, r := range out {
		if r.Status != nil {
			c.idToURL[r.Status.ID] = r.URL
		}
	}
	c.mu.Unlock()
	return out
}

func (c *Cluster) status(ctx context.Context, ep string) (*api.Status, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep+"/status", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status: HTTP %d", resp.StatusCode)
	}
	var st api.Status
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil, err
	}
	return &st, nil
}

// Leader returns the node that reports itself leader. If two do (an old
// leader that hasn't noticed yet), the higher term wins.
func (c *Cluster) Leader(ctx context.Context) (id, url string, err error) {
	var best *StatusResult
	for _, r := range c.Statuses(ctx) {
		if r.Status != nil && r.Status.Role == "leader" && (best == nil || r.Status.Term > best.Status.Term) {
			best = &r
		}
	}
	if best == nil {
		return "", "", errors.New("no node reports itself leader")
	}
	return best.Status.ID, best.URL, nil
}

// start picks where to send a leader-only request: the remembered leader,
// else whoever reports itself leader, else the first reachable node.
func (c *Cluster) start(ctx context.Context) (string, error) {
	c.mu.Lock()
	u := c.leaderURL
	c.mu.Unlock()
	if u != "" {
		return u, nil
	}
	if _, u, err := c.Leader(ctx); err == nil {
		return u, nil
	}
	eps, err := c.endpoints(ctx)
	if err != nil || len(eps) == 0 {
		return "", fmt.Errorf("no endpoints: %v", err)
	}
	return eps[0], nil
}

func (c *Cluster) urlFor(ctx context.Context, id string) string {
	c.mu.Lock()
	u, ok := c.idToURL[id]
	c.mu.Unlock()
	if ok {
		return u
	}
	c.Statuses(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.idToURL[id]
}

func (c *Cluster) remember(u string) { c.mu.Lock(); c.leaderURL = u; c.mu.Unlock() }

// send performs a leader-only request, following up to 3 redirects.
// It returns the final response (body unread, caller closes) or error.
func (c *Cluster) send(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	target, err := c.start(ctx)
	if err != nil {
		return nil, err
	}
	for hop := 0; ; hop++ {
		req, err := http.NewRequestWithContext(ctx, method, target+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			c.remember("")
			return nil, err
		}
		if resp.StatusCode != http.StatusMisdirectedRequest || hop == maxRedirects {
			if resp.StatusCode < 300 {
				c.remember(target)
			} else {
				c.remember("")
			}
			return resp, nil
		}
		next := c.urlFor(ctx, resp.Header.Get(api.HeaderLeader))
		resp.Body.Close()
		if next == "" {
			c.remember("")
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody}, nil
		}
		target = next
	}
}

// Put writes key=value through the leader and classifies the result.
func (c *Cluster) Put(ctx context.Context, key, value string) (Outcome, error) {
	resp, err := c.send(ctx, http.MethodPut, "/kv/"+escapeKey(key), []byte(value))
	if err != nil {
		return Classify(nil, err), err
	}
	defer resp.Body.Close()
	out := Classify(resp, nil)
	if out != OK {
		b, _ := io.ReadAll(resp.Body)
		return out, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}
	return OK, nil
}

// Get does a linearizable read through the leader.
func (c *Cluster) Get(ctx context.Context, key string) (string, bool, error) {
	resp, err := c.send(ctx, http.MethodGet, "/kv/"+escapeKey(key), nil)
	if err != nil {
		return "", false, err
	}
	return readValue(resp)
}

// GetStale reads key from one specific node without asking the leader.
func (c *Cluster) GetStale(ctx context.Context, nodeURL, key string) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nodeURL+"/kv/"+escapeKey(key)+"?stale=true", nil)
	if err != nil {
		return "", false, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", false, err
	}
	return readValue(resp)
}

func readValue(resp *http.Response) (string, bool, error) {
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		return string(b), true, nil
	case http.StatusNotFound:
		return "", false, nil
	default:
		return "", false, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}
}

func (c *Cluster) Join(ctx context.Context, id, addr string) error {
	return c.admin(ctx, "/join", api.JoinRequest{ID: id, Addr: addr})
}

func (c *Cluster) Leave(ctx context.Context, id string) error {
	return c.admin(ctx, "/leave", api.LeaveRequest{ID: id})
}

func (c *Cluster) admin(ctx context.Context, path string, v any) error {
	body, _ := json.Marshal(v)
	resp, err := c.send(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: HTTP %d: %s", path, resp.StatusCode, bytes.TrimSpace(b))
	}
	return nil
}

// escapeKey escapes each path segment but keeps '/' so keys like a/b/c work.
func escapeKey(k string) string {
	return (&url.URL{Path: k}).EscapedPath()
}
