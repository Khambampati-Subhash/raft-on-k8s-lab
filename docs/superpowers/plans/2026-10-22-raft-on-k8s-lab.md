# raft-on-k8s-lab Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go KV service on `hashicorp/raft`, deployed on kind, broken on purpose with Chaos Mesh, with a plain-English journey doc for every stage and experiment.

**Architecture:** Pure state machine (`internal/fsm`) → Raft wrapper (`internal/node`) → HTTP API (`internal/httpapi`) depending only on a `Store` interface. Shared wire types in `internal/api`; a leader-following HTTP client (`internal/client`) used by loadgen, raftwatch and raftctl. One container image holds all binaries. Plain kustomize manifests; Chaos Mesh via Helm.

**Tech Stack:** Go 1.25, `github.com/hashicorp/raft` (latest v1.x), `github.com/hashicorp/raft-boltdb/v2`, stdlib `net/http` (Go 1.22+ pattern mux), kind, kubectl/kustomize, Helm, Chaos Mesh 2.x, distroless static image (linux/arm64).

**Spec:** `docs/superpowers/specs/2026-10-22-raft-on-k8s-lab-design.md`

## Global Constraints

- Module path: `github.com/Khambampati-Subhash/raft-on-k8s-lab`. Go 1.25.
- Ports: HTTP `:8080`, Raft `:7000`. Namespace `raftlab`. kind cluster name `raftlab`.
- Server ID = pod name (`kv-0`). Raft advertise = `<id>.kv.raftlab.svc.cluster.local:7000`.
- Default `SnapshotThreshold` = 1024; other Raft timeouts = library defaults; all exposed as flags.
- HTTP codes exactly as spec §5.3. 504 body is literally `outcome-unknown`.
- **Deviation from spec §5.3 (decided in planning):** `X-Raft-Leader` header carries the leader **server ID**, not an address (Raft only knows the leader's Raft address, not its HTTP address). Clients resolve ID → URL via `/status` discovery.
- **Addition to spec §5.2 (decided in planning):** `raft.NewTCPTransport` only accepts an IP `*net.TCPAddr` as advertise, which would silently advertise the pod IP. We use `raft.NewNetworkTransport` with a custom stream layer that advertises the DNS name and resolves it on every dial.
- Loadgen outcome classes: `ok` / `failed` / `indeterminate`, exactly per spec §5.4. Lost > 0 ⇒ non-zero exit.
- Docs: every "it broke" claim is real pasted output; refuted hypotheses are kept. Journey format *What we tried → What broke → Why → What we changed → What we learned*; experiment format *Hypothesis → Injection → Observed → Explanation → Takeaway*.
- No TLS, auth, Prometheus, Grafana, Helm chart for our app.
- Pushing to GitHub / creating the repo / `brew install` require explicit user confirmation at that step.

## Review Focus

1. **Keys containing `/`** (loadgen keys are `<runID>/<worker>/<seq>`): `PUT /kv/a/b/c` must address key `a/b/c`, not 404. → `TestKeyWithSlashes` in Task 5 (use `{key...}` wildcard).
2. **Pods not yet ready must be resolvable by DNS** — otherwise peers can't reach each other, no leader forms, readiness never passes: deadlock. → headless Service `publishNotReadyAddresses: true`; assertion in Task 12 smoke script.
3. **Restart with existing state** must neither re-bootstrap nor lose term/data. → `TestRestartWithExistingStateKeepsDataAndTerm` in Task 3.
4. **Re-join after restart** (same ID, same address) must be a harmless no-op; same ID with a new address must update it. → `TestJoinIsIdempotent`, `TestJoinSameIDNewAddrUpdates` in Task 4.
5. **Connection reset after the request was sent** must be `indeterminate`, never `failed`; redirect chains capped at 3 hops. → `TestClassifyConnResetIsIndeterminate`, `TestPutRedirectCappedAt3` in Task 6.

---

## Phase A — Local (stages 0–2)

### Task 1: Repo scaffold, naive KV, stages 0–1

**Files:**
- Create: `go.mod`, `.gitignore`, `LICENSE` (MIT, 2026, Khambampati-Subhash), `README.md` (stub: "learning project — not production software"), `Makefile`
- Create: `cmd/naivekv/main.go`, `cmd/naivekv/main_test.go`
- Create: `scripts/stage0-demo.sh`, `scripts/stage1-demo.sh`
- Create: `docs/journey/00-single-node.md`, `docs/journey/01-naive-replication.md`, `docs/glossary.md`

**Interfaces:**
- Produces: `naivekv` binary — flags `--http :8080`, `--peers http://h:p,...`. `PUT/GET /kv/{key...}`; with peers, PUT fans out best-effort async to `peer + /kv/{key}?replica=1` (replica writes don't fan out again). Makefile targets `test`, `build`.

- [ ] **Step 1: Ask the user to confirm creating the public GitHub repo** `Khambampati-Subhash/raft-on-k8s-lab`. On yes: `gh repo create Khambampati-Subhash/raft-on-k8s-lab --public --source . --description "Learning lab: Raft (hashicorp/raft) on Kubernetes, broken on purpose with Chaos Mesh" --push`.
- [ ] **Step 2: Write failing tests** in `cmd/naivekv/main_test.go` using `httptest`:
  - `TestPutThenGet`: PUT `a/b`=`1` → 204; GET `a/b` → 200 body `1`.
  - `TestGetMissingIs404`.
  - `TestReplicaWriteDoesNotFanOut`: server with a peer pointing at a counting `httptest` server; PUT with `?replica=1` → peer counter stays 0; PUT without → peer counter becomes 1 (poll ≤1s).
- [ ] **Step 3: Run** `go test ./cmd/naivekv/...` → FAIL (undefined handler).
- [ ] **Step 4: Implement** `newHandler(peers []string, hc *http.Client) http.Handler` in `main.go`; `map[string]string` + `sync.RWMutex`; fan-out in goroutines, errors logged and ignored (that's the point).
- [ ] **Step 5: Run** tests → PASS.
- [ ] **Step 6: Write `scripts/stage0-demo.sh`**: start one naivekv, write 3 keys, `kill -9`, restart, GET → 404. Run it; paste output into `docs/journey/00-single-node.md`.
- [ ] **Step 7: Write `scripts/stage1-demo.sh`**: start 3 naivekv with full peer lists on 8081–8083. (a) kill node 3, write `k1`, restart node 3, GET `k1` on node 3 → 404. (b) concurrently PUT `x=A` to node 1 and `x=B` to node 2 (10 rounds), then GET `x` from all three → print values; show at least one round where they differ. Run it; paste output into `docs/journey/01-naive-replication.md` (why: no agreed order, no catch-up → need consensus).
- [ ] **Step 8: Start `docs/glossary.md`** with: replica, replication, consistency, split brain, consensus. Plain English, one or two sentences each.
- [ ] **Step 9: Commit and tag**
```bash
git add -A && git commit -m "Stage 0-1: naive KV and its failures"
git tag stage-0 && git tag stage-1
```

### Task 2: `internal/fsm` + `internal/api`

**Files:**
- Create: `internal/api/types.go`, `internal/fsm/fsm.go`, `internal/fsm/fsm_test.go`

**Interfaces:**
- Produces (`internal/api`):
```go
const HeaderLeader = "X-Raft-Leader"
type Server struct { ID, Addr string; Suffrage string }
type Status struct {
    ID, Role string            // Role: "leader" | "follower" | "candidate" | "shutdown"
    Term uint64
    LeaderID, LeaderAddr string
    CommitIndex, AppliedIndex, LastLogIndex uint64
    LastContact string         // raft Stats() value verbatim
    Servers []Server
}
type JoinRequest struct { ID string `json:"id"`; Addr string `json:"addr"` }
type LeaveRequest struct { ID string `json:"id"` }
```
  (JSON tags lowerCamel for all fields.)
- Produces (`internal/fsm`):
```go
type Command struct { Op, Key, Value string }   // json: op, key, value
const OpSet, OpDelete = "set", "delete"
func EncodeCommand(c Command) ([]byte, error)
func New() *KV
func (k *KV) Apply(l *raft.Log) any     // nil on success, error on bad command; never panics
func (k *KV) Get(key string) (string, bool)
func (k *KV) Snapshot() (raft.FSMSnapshot, error)
func (k *KV) Restore(rc io.ReadCloser) error
```

- [ ] **Step 1: `go get github.com/hashicorp/raft@latest github.com/hashicorp/raft-boltdb/v2@latest`**; record versions in commit message.
- [ ] **Step 2: Write failing tests:**
  - `TestApplySetThenGet`, `TestApplyDeleteRemovesKey`.
  - `TestApplyBadCommandReturnsErrorAndKeepsState`: Apply `{"op":"nope"}` and non-JSON bytes → result is `error`; previously set key still present.
  - `TestSnapshotRestoreRoundTrip`: set 100 keys, Snapshot, `Persist` into a `bytes.Buffer`-backed fake `raft.SnapshotSink`, Restore into `New()` → all 100 equal.
  - `TestSnapshotIsolatedFromLaterWrites`: Snapshot, then Apply set `k=new`, then Persist → restored value of `k` is the old one.
- [ ] **Step 3: Run** `go test ./internal/fsm/...` → FAIL.
- [ ] **Step 4: Implement.** Snapshot copies the map under read lock; Persist JSON-encodes the copy, `sink.Cancel()` on error. Restore decodes then swaps the map under write lock.
- [ ] **Step 5: Run** → PASS. **Commit** `feat(fsm): KV state machine with snapshots`.

### Task 3: `internal/node` — transport, core, replication, failover

**Files:**
- Create: `internal/node/transport.go`, `internal/node/transport_test.go`
- Create: `internal/node/node.go`, `internal/node/errors.go`, `internal/node/storage.go`
- Create: `internal/node/cluster_test.go` (helper), `internal/node/node_integration_test.go`

**Interfaces:**
- Consumes: `fsm.New`, `fsm.EncodeCommand`, `api.Status`.
- Produces:
```go
func NewDNSTransport(bind, advertise string, logOut io.Writer) (*raft.NetworkTransport, error)
type Config struct {
    ID, Advertise string
    Bootstrap bool
    HeartbeatTimeout, ElectionTimeout, LeaderLeaseTimeout, CommitTimeout, ApplyTimeout time.Duration // 0 → library default; ApplyTimeout 0 → 5s
    SnapshotThreshold, TrailingLogs uint64 // 0 → 1024 / library default
}
type Deps struct { Logs raft.LogStore; Stable raft.StableStore; Snaps raft.SnapshotStore; Transport raft.Transport }
func NewDiskDeps(dataDir, bind, advertise string, logOut io.Writer) (Deps, io.Closer, error) // boltdb at dataDir/raft.db, snapshots dataDir/snapshots retain 2
func New(cfg Config, d Deps) (*Node, error)
func (n *Node) Set(ctx context.Context, key, value string) error
func (n *Node) Delete(ctx context.Context, key string) error
func (n *Node) Get(ctx context.Context, key string, stale bool) (string, bool, error)
func (n *Node) Status() api.Status
func (n *Node) Shutdown() error
var ErrNoLeader = errors.New("no leader")
var ErrTimeout  = errors.New("outcome unknown")
type NotLeaderError struct{ LeaderID string }   // Error(): "not leader; leader is <id>"
```
- Error mapping from raft: `raft.ErrNotLeader` → `NotLeaderError{leader}` or `ErrNoLeader` if leader unknown; `raft.ErrLeadershipLost`, `raft.ErrLeadershipTransferInProgress`, and apply-future timeout → `ErrTimeout`; `raft.ErrEnqueueTimeout` → `ErrNoLeader` (definitely not appended).

- [ ] **Step 1: Write failing transport test** `TestDNSTransportAdvertisesHostname`: `NewDNSTransport("127.0.0.1:0", "localhost:<port>")` (pick free port first) → `LocalAddr() == "localhost:<port>"`; a second transport can `AppendEntries` to it (or simply: dial `localhost:<port>` succeeds through the stream layer's `Dial`).
- [ ] **Step 2: Implement** `dnsStreamLayer{ net.Listener; advertise dnsAddr }` where `dnsAddr` implements `net.Addr` (`Network()="tcp"`, `String()=advertise`), `Dial(addr, timeout)` = `net.DialTimeout("tcp", string(addr), timeout)`. `NewDNSTransport` = `raft.NewNetworkTransport(layer, 3, 10*time.Second, logOut)`. Run → PASS.
- [ ] **Step 3: Write test helper** in `cluster_test.go`: `newTestCluster(t, n int) *testCluster` — inmem stores (`raft.NewInmemStore`, `raft.NewInmemSnapshotStore`), `raft.NewInmemTransport`, all transports `Connect`ed pairwise, node 0 `Bootstrap=true` with fast timeouts (heartbeat/election/lease 50ms, commit 5ms), others added via `Join` (Task 4 — until then the helper bootstraps all `n` servers in one `BootstrapCluster` config on node 0). Methods: `leader(t) *Node` (waits ≤3s), `followers()`, `eventually(t, cond func() bool)`.
- [ ] **Step 4: Write failing integration tests:**
  - `TestSingleNodeBootstrapBecomesLeader`: n=1 → `Status().Role == "leader"`, `Term >= 1`.
  - `TestReplicationToFollowers`: n=3, `Set(k,v)` on leader → eventually every follower `Get(k, stale=true) == v`.
  - `TestWriteOnFollowerReturnsNotLeader`: `errors.As(err, &NotLeaderError{})` with `LeaderID == leader.Status().ID`.
  - `TestLinearizableGetOnFollowerReturnsNotLeader`; `TestStaleGetOnFollowerSucceeds`.
  - `TestLeaderFailover`: Set `k`, `Shutdown` leader → a different leader within 3s, `Get(k, false) == v` on it, `Term` strictly greater.
  - `TestIsolatedLeaderWriteNeverReportsSuccess`: n=3, disconnect leader's transport from both followers (`DisconnectAll` + peers disconnect from it), `Set` with ctx 2s → `err != nil` and is `ErrTimeout`, `ErrNoLeader`, or `NotLeaderError`.
  - `TestRestartWithExistingStateKeepsDataAndTerm`: n=1 with `Bootstrap=true`, Set k, record term, Shutdown, `New` again with **same** stores and `Bootstrap=true` → no error, becomes leader, `Get(k)` ok, term ≥ recorded.
- [ ] **Step 5: Run** `go test ./internal/node/... -race` → FAIL.
- [ ] **Step 6: Implement** `New`: build `raft.DefaultConfig()` with overrides, `LocalID=cfg.ID`; if `cfg.Bootstrap` and `!raft.HasExistingState(...)` → `BootstrapCluster` with self `{ID, Transport.LocalAddr()}`. `Set/Delete`: `raft.Apply(cmd, ApplyTimeout)`, honor `ctx` deadline if shorter; check both future error and FSM response error. `Get(stale=false)`: `VerifyLeader().Error()` mapped as above, then local read. `Status()`: `State()`, `LeaderWithID()`, `Stats()` parsed (`term`, `commit_index`, `applied_index`, `last_log_index`, `last_contact`), `GetConfiguration()`. `NewDiskDeps` uses `raftboltdb.NewBoltStore` (same store for logs+stable) and `raft.NewFileSnapshotStore(dir/snapshots, 2, logOut)`.
- [ ] **Step 7: Run** → PASS (run 5× with `-count=5` to catch flakes).
- [ ] **Step 8: Commit** `feat(node): raft wrapper with DNS transport, replication, failover`.

### Task 4: `internal/node` — membership and snapshot catch-up

**Files:**
- Create: `internal/node/membership.go`, `internal/node/membership_test.go`
- Modify: `internal/node/cluster_test.go` (helper now joins followers via `Join`)

**Interfaces:**
- Produces:
```go
func (n *Node) Join(id, addr string) error   // leader only; NotLeaderError otherwise
func (n *Node) Leave(id string) error        // leader only
```

- [ ] **Step 1: Write failing tests:**
  - `TestJoinAddsVoter`: 1-node cluster, Join node B → B in `Status().Servers` with `Suffrage=="Voter"`, B replicates a later write.
  - `TestJoinIsIdempotent`: Join B twice with same addr → nil both times, B listed once.
  - `TestJoinSameIDNewAddrUpdates`: Join B at addr1 then addr2 → listed once with addr2.
  - `TestLeaveRemovesServer`: 3 nodes, Leave(follower) → not in Servers; remaining 2 still commit writes.
  - `TestJoinOnFollowerReturnsNotLeader`.
  - `TestLaggingFollowerCatchesUpViaSnapshot`: `SnapshotThreshold=10, TrailingLogs=5`; disconnect follower F, write 100 keys, force `raft.Snapshot()` on leader, reconnect F → eventually F stale-reads all 100.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** Join: read config; same ID+addr → nil; same ID other addr or same addr other ID → `RemoveServer` first; then `AddVoter(id, addr, 0, 0)`. Leave: `RemoveServer(id, 0, 0)`. Map errors as Task 3. Switch the test helper to bootstrap only node 0 and Join the rest.
- [ ] **Step 4: Run** `go test ./internal/node/... -race -count=3` → PASS. **Commit** `feat(node): join/leave and snapshot catch-up`.

### Task 5: `internal/httpapi`

**Files:**
- Create: `internal/httpapi/server.go`, `internal/httpapi/server_test.go`

**Interfaces:**
- Consumes: `api.*`, error values/types from `internal/node`.
- Produces:
```go
type Store interface {
    Set(ctx context.Context, key, value string) error
    Delete(ctx context.Context, key string) error
    Get(ctx context.Context, key string, stale bool) (string, bool, error)
    Join(id, addr string) error
    Leave(id string) error
    Status() api.Status
}
func New(s Store) http.Handler
```
  Routes: `PUT /kv/{key...}`, `DELETE /kv/{key...}`, `GET /kv/{key...}`, `GET /status`, `POST /join`, `POST /leave`, `GET /healthz`, `GET /readyz`.

- [ ] **Step 1: Write failing table test** `TestStatusCodes` with a `fakeStore` returning configured errors. Rows (method, path, store error → code, header):
  - PUT ok → 204; PUT `NotLeaderError{"kv-1"}` → 421, `X-Raft-Leader: kv-1`; PUT `ErrNoLeader` → 503; PUT `ErrTimeout` → 504 body `outcome-unknown`.
  - DELETE: same four rows.
  - GET found → 200 body value; not found → 404; `NotLeaderError` → 421; `ErrNoLeader` → 503.
  - GET `?stale=true` → store called with `stale=true` (assert recorded arg).
  - POST /join valid → 204; invalid JSON or empty id/addr → 400; `NotLeaderError` → 421. Same for /leave (empty id → 400).
  - /readyz with `Status().LeaderID==""` → 503, else 200. /healthz → 200 always. /status → 200 JSON decodes into `api.Status`.
  - Any other store error → 500.
- [ ] **Step 2: Write** `TestKeyWithSlashes`: PUT `/kv/run1/0/42` → fake records key `run1/0/42`.
- [ ] **Step 3: Run** → FAIL. **Step 4: Implement** (request ctx passed to store; body read with `http.MaxBytesReader` 1 MiB → 413 on overflow). **Step 5: Run** → PASS.
- [ ] **Step 6: Commit** `feat(httpapi): KV and admin endpoints`.

### Task 6: `internal/client` — discovery, leader following, classification

**Files:**
- Create: `internal/client/client.go`, `internal/client/classify.go`, `internal/client/client_test.go`

**Interfaces:**
- Consumes: `api.Status`, `api.HeaderLeader`, `api.JoinRequest/LeaveRequest`.
- Produces:
```go
type Outcome int
const ( OK Outcome = iota; Failed; Indeterminate )
func (o Outcome) String() string                  // "ok" | "failed" | "indeterminate"
func Classify(resp *http.Response, err error) Outcome
type StatusResult struct { URL string; Status *api.Status; Err error }
type Cluster struct { /* unexported */ }
func NewStatic(urls []string, hc *http.Client) *Cluster
func NewDNS(host string, port int, hc *http.Client) *Cluster   // resolves A records on every Statuses() call
func (c *Cluster) Statuses(ctx context.Context) []StatusResult // parallel GET /status, sorted by Status.ID (unreachable last, by URL)
func (c *Cluster) Put(ctx context.Context, key, value string) (Outcome, error)
func (c *Cluster) Get(ctx context.Context, key string) (string, bool, error)          // linearizable, via leader
func (c *Cluster) GetStale(ctx context.Context, url, key string) (string, bool, error)
func (c *Cluster) Leader(ctx context.Context) (id, url string, err error)
func (c *Cluster) Join(ctx context.Context, id, addr string) error
func (c *Cluster) Leave(ctx context.Context, id string) error
```
- Rules: `Classify`: `err` is a dial error (`*net.OpError` with `Op=="dial"`) → Failed; any other err → Indeterminate; 2xx → OK; 421/503/400/404 → Failed; 504 and other 5xx → Indeterminate. Put/Join/Leave start at cached leader URL (or first node), follow `X-Raft-Leader` by mapping ID → URL from `Statuses`, **max 3 hops**, then return last outcome.

- [ ] **Step 1: Write failing tests:**
  - `TestClassifyTable`: rows for 204, 421, 503, 504, 500, nil resp + dial-refused error (dial a closed port), nil resp + `context.DeadlineExceeded`.
  - `TestClassifyConnResetIsIndeterminate`: `httptest` server whose handler hijacks and closes the connection; real `http.Client` PUT → `Indeterminate`.
  - `TestPutFollowsLeaderHint`: two fake nodes; A answers 421 `X-Raft-Leader: b`, both `/status` report IDs a/b; B answers 204 → `OK`, B received the PUT.
  - `TestPutRedirectCappedAt3`: both nodes always 421 pointing at each other → `Failed` after exactly 4 PUT requests total (initial + 3).
  - `TestStatusesSortedAndIncludesUnreachable`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(client): leader-following client with outcome classification`.

### Task 7: `cmd/kvnode` + stage 2 local cluster

**Files:**
- Create: `cmd/kvnode/main.go`, `cmd/kvnode/join.go`, `cmd/kvnode/join_test.go`
- Create: `scripts/local-up.sh`, `scripts/local-down.sh`, `docs/journey/02-raft-local.md`
- Modify: `Makefile` (`local-up`, `local-down`), `docs/glossary.md`

**Interfaces:**
- Consumes: `node.New`, `node.NewDiskDeps`, `node.NewDNSTransport` (via NewDiskDeps), `httpapi.New`, `client.NewStatic(...).Join`.
- Produces: `kvnode` flags: `--id` (default `$POD_NAME`), `--http :8080`, `--raft-bind :7000`, `--raft-advertise`, `--data /data`, `--bootstrap auto|true|false` (auto ⇒ true iff id ends in `-0`), `--join` (comma-separated HTTP URLs), `--heartbeat-timeout`, `--election-timeout`, `--leader-lease-timeout`, `--apply-timeout 5s`, `--snapshot-threshold 1024`.
```go
func resolveBootstrap(mode, id string) (bool, error)   // in join.go
func joinLoop(ctx context.Context, j Joiner, self string, selfAddr string, isMember func() bool, backoff Backoff) // retries until success or isMember()
type Joiner interface { Join(ctx context.Context, id, addr string) error }
type Backoff struct { Initial, Max time.Duration } // 200ms, 5s, doubling
```

- [ ] **Step 1: Write failing tests:** `TestResolveBootstrap` (auto+`kv-0`→true, auto+`kv-1`→false, auto+`node-10`→false, true/false literal, `bogus`→error); `TestJoinLoopRetriesUntilSuccess` (fake fails 3× then nil → 4 calls); `TestJoinLoopStopsWhenAlreadyMember` (isMember true → 0 calls).
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement** `join.go`, then `main.go`: build deps, node, HTTP server; start `joinLoop` in a goroutine when not bootstrapping and `--join` set; on SIGTERM: `http.Server.Shutdown(10s)`, `node.Shutdown()`, close deps; fatal startup errors → `os.Exit(1)`. **Step 4: Run** → PASS.
- [ ] **Step 5: Write `scripts/local-up.sh`**: build, start 3 kvnodes (`n1..n3`, raft `127.0.0.1:7001-7003`, http `:8081-8083`, data `${TMPDIR}/raftlab/nX`), n1 `--bootstrap=true`, n2/n3 `--join=http://127.0.0.1:8081`; wait until all `/readyz` 200. `local-down.sh` kills them and deletes data.
- [ ] **Step 6: Verify manually:** `make local-up`; PUT via n1; stale GET from n2/n3; kill the leader; show another node becomes leader with a higher term (`curl /status`); GET still returns value. Paste the real output into `docs/journey/02-raft-local.md` (explain term, log, commit, quorum, leader election in plain words). Add those terms to the glossary.
- [ ] **Step 7: Commit, tag, push** (`git tag stage-2`; ask before first push of tags if not already confirmed in Task 1).

### Task 8: `cmd/raftwatch` + `cmd/raftctl`

**Files:**
- Create: `internal/watch/diff.go`, `internal/watch/render.go`, `internal/watch/watch_test.go`
- Create: `cmd/raftwatch/main.go`, `cmd/raftctl/main.go`

**Interfaces:**
- Consumes: `client.Cluster`, `client.StatusResult`.
- Produces:
```go
type Event struct { At time.Time; Kind, Node, Detail string } // Kind: "leader-change" | "term" | "unreachable" | "reachable" | "role"
func Diff(prev, cur []client.StatusResult, now time.Time) []Event   // pure
func Render(w io.Writer, cur []client.StatusResult)                // table: node, role, term, leader, commit, applied, last-contact, reachable
```
  `raftwatch` flags: `--service` + `--port` (DNS mode) or `--nodes` (static), `--interval 250ms`, `--events-only`, `--for 0` (0 = forever). Output event lines: `<RFC3339Nano> <kind> <node> <detail>`.
  `raftctl` subcommands (same discovery flags): `leader` (prints leader ID), `status` (JSON), `leave <id>`.

- [ ] **Step 1: Write failing tests:** `TestDiffLeaderChange` (kv-0 → kv-1 emits one `leader-change` with detail `kv-0 -> kv-1`), `TestDiffTermBump`, `TestDiffUnreachableThenReachable`, `TestDiffNoChangeNoEvents`, `TestRenderContainsAllNodes`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement** `internal/watch`, then the two thin mains. **Step 4: Run** → PASS.
- [ ] **Step 5: Verify** against `make local-up` with `--nodes`: kill leader, see `leader-change` and `term` events. **Commit** `feat: raftwatch and raftctl`.

### Task 9: `cmd/loadgen`

**Files:**
- Create: `internal/loadgen/run.go`, `internal/loadgen/verify.go`, `internal/loadgen/report.go`, `internal/loadgen/loadgen_test.go`
- Create: `cmd/loadgen/main.go`

**Interfaces:**
- Consumes: `client.Cluster`, `client.Outcome`.
- Produces:
```go
type Writer interface { Put(ctx context.Context, key, value string) (client.Outcome, error) }
type Reader interface { Get(ctx context.Context, key string) (string, bool, error) }
type Record struct { Key, Value string; Outcome client.Outcome; Start, End time.Time; Err string }
type Window struct { From, To time.Time }
type Report struct {
    RunID string; Total, OK, Failed, Indeterminate, Lost int
    IndeterminatePresent, IndeterminateAbsent int
    LostKeys []string; P50, P99 time.Duration; Gaps []Window
    StaleLag map[string]uint64 // pod ID → max observed lag; nil if probe off
}
func Run(ctx context.Context, w Writer, workers int, d time.Duration, runID string) []Record
func Verify(ctx context.Context, recs []Record, r Reader, retryFor time.Duration) Report // retries each read on error up to retryFor total
func Gaps(recs []Record, threshold time.Duration) []Window   // gaps between consecutive OK completions > threshold
func (r Report) Summary() string
```
  `loadgen` flags: discovery flags, `--workers 4`, `--duration 60s`, `--run-id` (default timestamp), `--report /tmp/report.json`, `--stale-probe`, `--gap 500ms`, `--verify-wait 60s`. Prints `Summary()` + JSON to stdout; exit 1 if `Lost > 0`, exit 2 if verify couldn't reach a leader.
  Stale probe: separate goroutine writes key `<runID>/probe` = counter via leader every 100ms; every 500ms reads it `GetStale` from each pod; lag = last-acked counter − read counter, max per pod.

- [ ] **Step 1: Write failing tests:** `TestVerifyCountsLostAck` (OK record, reader returns not found → Lost 1, LostKeys has key); `TestVerifyWrongValueIsLost`; `TestVerifyIndeterminateNotLost` (present/absent counted, Lost 0); `TestVerifyFailedNotChecked` (reader never called for Failed); `TestGapsDetectsWindow` (OK at t=0, 100ms, 900ms, 1000ms with threshold 500ms → one window 100ms–900ms); `TestPercentiles` (latencies 1..100ms → P50 50ms, P99 99ms); `TestRunKeysUnique` (fake writer, 4 workers → no duplicate keys, all prefixed `runID/`).
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → PASS.
- [ ] **Step 5: Verify locally:** `make local-up`, run loadgen 30s with `--nodes`, kill the leader at ~10s → report shows `Lost: 0`, ≥1 gap. **Commit** `feat: loadgen workload and checker`. **Push** (phase A done).

## Phase B — Kubernetes (stages 3–4)

### Task 10: Image, kind clusters, Makefile infrastructure

**Files:**
- Create: `Dockerfile`, `.dockerignore`, `deploy/kind/cluster-3.yaml`, `deploy/kind/cluster-5.yaml`
- Modify: `Makefile`

**Interfaces:**
- Produces: image `raftlab:$(git describe --always --dirty)` containing `/kvnode /loadgen /raftwatch /raftctl /naivekv`, user 65532. Make targets: `tools`, `image`, `cluster-up`, `cluster-up-5`, `cluster-down`, `load-image`. Variable `IMAGE`.

- [ ] **Step 1: Ask the user to confirm** `brew install kind helm`; run on yes. `make tools` checks `kind`, `helm`, `kubectl`, `docker` on PATH and prints Docker's memory (`docker info --format '{{.MemTotal}}'`); fails if < 8 GiB.
- [ ] **Step 2: Write `Dockerfile`**: `golang:1.25` builder, `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath` all five `cmd/*`; final `gcr.io/distroless/static-debian12:nonroot`.
- [ ] **Step 3: kind configs**: name `raftlab`, 1 control-plane + 3 (resp. 5) workers.
- [ ] **Step 4: Verify:** `make cluster-up image load-image` → `kubectl get nodes` shows 4 Ready nodes; `docker exec raftlab-worker crictl images | grep raftlab` lists the image. `make cluster-down` removes it. **Commit** `build: image and kind clusters`.

### Task 11: Stage 3 — the deliberately wrong Deployment

**Files:**
- Create: `deploy/stage3-deployment/kustomization.yaml`, `namespace.yaml`, `seed.yaml`, `rest.yaml`, `service.yaml`, `raftwatch.yaml`
- Create: `scripts/stage3-demo.sh`, `docs/journey/03-raft-on-deployment.md`
- Modify: `Makefile` (`stage3-deploy`, `stage3-demo`)

**Interfaces:**
- Consumes: image from Task 10; raftwatch DNS mode.
- Produces: Deployment `kv-seed` (1 replica, `--bootstrap=true`) and `kv-rest` (2 replicas, `--bootstrap=false`, `--join=http://kv-seed:8080`); both `--id=$(POD_NAME)`, `--raft-advertise=$(POD_IP):7000`, data on `emptyDir`. Services `kv-seed` (ClusterIP → seed pod) and `kv` (headless, all kv pods, `publishNotReadyAddresses: true`) for raftwatch.

- [ ] **Step 1: Write manifests** per Interfaces.
- [ ] **Step 2: Write `scripts/stage3-demo.sh`**: deploy; wait for 1 leader + 3 voters (`raftctl status`); write keys; (a) delete both `kv-rest` pods → new pods with new names/IPs join, `raftctl status` shows **5 servers, 2 dead**; (b) delete `kv-seed` pod → it comes back empty with `--bootstrap=true` and forms its **own 1-node cluster**: raftwatch shows two leaders; GET keys on new seed → 404. Print `raftctl status` after each step.
- [ ] **Step 3: Run it** on a fresh 3-worker cluster; paste real output into `docs/journey/03-raft-on-deployment.md` (why: identity tied to pod name/IP, storage tied to pod lifetime, who bootstraps is ambiguous).
- [ ] **Step 4: Commit and tag** `stage-3`.

### Task 12: Stage 4 — StatefulSet base + smoke

**Files:**
- Create: `deploy/base/kustomization.yaml`, `namespace.yaml`, `statefulset.yaml`, `services.yaml`, `pdb.yaml`, `raftwatch.yaml`
- Create: `scripts/smoke.sh`, `docs/journey/04-raft-on-statefulset.md`
- Modify: `Makefile` (`deploy`, `undeploy`, `smoke`, `watch`)

**Interfaces:**
- Produces (exact values):
  - StatefulSet `kv`, `serviceName: kv`, `replicas: 3`, `podManagementPolicy: Parallel`, labels `app: kv`.
  - Pod: `securityContext {runAsNonRoot: true, runAsUser: 65532, fsGroup: 65532}`; required `podAntiAffinity` on `kubernetes.io/hostname`; `terminationGracePeriodSeconds: 30`; resources req `50m/64Mi`, limit `256Mi`.
  - Args: `--id=$(POD_NAME) --raft-advertise=$(POD_NAME).kv.raftlab.svc.cluster.local:7000 --bootstrap=auto --join=http://kv-0.kv.raftlab.svc.cluster.local:8080,http://kv-1.kv.raftlab.svc.cluster.local:8080,http://kv-2.kv.raftlab.svc.cluster.local:8080 --data=/data`.
  - Probes: liveness `GET /healthz` period 5s failureThreshold 3; readiness `GET /readyz` period 2s.
  - `volumeClaimTemplates`: `data`, 1Gi, RWO, mounted at `/data`.
  - Service `kv`: `clusterIP: None`, `publishNotReadyAddresses: true`, ports http 8080 / raft 7000. Service `kv-client`: ClusterIP, port 8080.
  - PDB `kv`: `maxUnavailable: 1`.
  - Deployment `raftwatch` (1 replica) running `/raftwatch --service kv.raftlab.svc.cluster.local --port 8080`, same image. `make watch` = `kubectl -n raftlab exec -it deploy/raftwatch -- /raftwatch --service kv.raftlab.svc.cluster.local --port 8080`.

- [ ] **Step 1: Write `scripts/smoke.sh`** (fails fast, non-zero exit):
  - assert `kubectl get svc kv -o jsonpath='{.spec.publishNotReadyAddresses}'` == `true` (Review Focus 2);
  - wait ≤120s for 3/3 Ready; assert three pods on three distinct nodes;
  - `raftctl status` → exactly one leader, 3 voters, all same term;
  - run loadgen Job 10s → `Lost: 0`;
  - delete `kv-1` pod → it returns Ready with the same ID, its keys still readable via stale GET on it (proves PVC + fsGroup).
- [ ] **Step 2: Write manifests**; `make deploy` = `kubectl apply -k deploy/base` + `kubectl -n raftlab set image statefulset/kv kvnode=$(IMAGE)` (and raftwatch).
- [ ] **Step 3: Run** `make cluster-up image load-image deploy smoke` → exit 0.
- [ ] **Step 4: Demonstrate the two traps** for the doc (real output, then revert): (a) set `podManagementPolicy: OrderedReady` and recreate → kv-0 stuck NotReady, kv-1 never created; (b) temporarily point liveness at `/readyz` with failureThreshold 1, kill the leader → `kubectl get pods -w` shows restarts across nodes. Paste both plus the working smoke output into `docs/journey/04-raft-on-statefulset.md`, walking spec §4's table.
- [ ] **Step 5: Commit, tag `stage-4`, push** (phase B done).

## Phase C — Chaos (stage 5+)

### Task 13: Chaos Mesh, experiment runner, E1–E2

**Files:**
- Create: `scripts/chaos-install.sh`, `scripts/label-leader.sh`, `scripts/run-loadgen.sh`, `scripts/exp.sh`
- Create: `chaos/E1-leader-kill.yaml`, `chaos/E2-follower-kill.yaml`
- Create: `docs/experiments/README.md` (template + index), `docs/experiments/E1-leader-kill.md`, `docs/experiments/E2-follower-kill.md`, `docs/experiments/results/.gitkeep`
- Modify: `Makefile` (`chaos-install`, `exp-%`)

**Interfaces:**
- Produces:
  - `chaos-install.sh`: `helm repo add chaos-mesh https://charts.chaos-mesh.org`; install pinned `CHAOS_MESH_VERSION` (latest 2.x at execution time, recorded in the Makefile) into ns `chaos-mesh` with `--set chaosDaemon.runtime=containerd --set chaosDaemon.socketPath=/run/containerd/containerd.sock`; wait for pods Ready.
  - `label-leader.sh`: removes `raft-role`/`raft-rank` labels from all kv pods, then labels `$(raftctl leader)` with `raft-role=leader,raft-rank=l`; the followers (sorted by ID) get `raft-role=follower` and `raft-rank=f1` / `f2`.
  - `run-loadgen.sh <runID> <duration>`: applies Job `loadgen-<runID>` (same image, `--service kv.raftlab.svc.cluster.local --port 8080 --duration <d> --stale-probe`, `backoffLimit: 0`).
  - `exp.sh <ID>`: label leader → start loadgen (120s) → start `raftwatch --events-only --for 150s` capture in background → sleep 20s → inject (`chaos/<ID>.sh` if it exists, else `kubectl apply -f chaos/<ID>-*.yaml`; YAMLs carry `duration: 40s`) → wait for Job → `kubectl delete -f` the chaos → write `docs/experiments/results/<ID>-<UTC timestamp>.log` (events + loadgen output + `raftctl status` before/after) → exit with Job's status.
  - E1: `PodChaos` `action: pod-kill`, `mode: one`, selector `app: kv, raft-role: leader`. E2: same with `raft-role: follower`.

- [ ] **Step 1: Write scripts and YAMLs.** `make chaos-install` → chaos-mesh pods Ready.
- [ ] **Step 2: Run** `make exp-E1` → results log written, exit 0 (Lost 0). If exit ≠ 0, follow systematic-debugging before touching anything.
- [ ] **Step 3: Run** `make exp-E2` → same.
- [ ] **Step 4: Write** `E1-leader-kill.md`, `E2-follower-kill.md` from the results logs (election time measured from raftwatch timestamps; gap windows from loadgen). State whether each hypothesis held.
- [ ] **Step 5: Commit** `chaos: runner, E1 leader kill, E2 follower kill` (results logs included).

### Task 14: E3–E4 — partitions

**Files:** Create `chaos/E3-minority-partition.yaml`, `chaos/E4-isolate-leader.yaml`, `docs/experiments/E3-minority-partition.md`, `docs/experiments/E4-isolate-leader.md`

**Interfaces:** `NetworkChaos` `action: partition`, `direction: both`, `duration: 40s`. E3: `selector` = `raft-rank: f1`, `target` = `app: kv` with `expressionSelectors: [{key: raft-rank, operator: NotIn, values: [f1]}]`, `mode: all`. E4: `selector` = `raft-role: leader`, `target` = `raft-role: follower`, `mode: all`. Only kv↔kv traffic is cut; loadgen/raftwatch can still reach every pod.

- [ ] **Step 1: Write YAMLs. Step 2: Run** `make exp-E3`, `make exp-E4` → logs written; Lost 0 expected.
- [ ] **Step 3: Write docs.** E3 must show the stale-probe lag on the isolated follower; E4 must show the old leader's term/role events and the new leader's higher term, and that writes sent to the old leader came back failed/indeterminate, never ok. Add *partition*, *stale read*, *leader lease* to glossary. **Commit.**

### Task 15: E5–E6 — latency and packet loss

**Files:** Create `chaos/E5a-latency-100ms.yaml`, `chaos/E5b-latency-800ms.yaml`, `chaos/E6a-loss-10.yaml`, `chaos/E6b-loss-40.yaml`, `docs/experiments/E5-latency.md`, `docs/experiments/E6-packet-loss.md`

**Interfaces:** `NetworkChaos` on all kv pods (`mode: all`), `target` all kv pods, `direction: both`, `duration: 40s`. E5a `delay {latency: 100ms, jitter: 50ms}`; E5b `delay {latency: 800ms, jitter: 100ms}`; E6a `loss {loss: "10"}`; E6b `loss {loss: "40"}`. `exp.sh` accepts sub-IDs (`make exp-E5a`).

- [ ] **Step 1: Write YAMLs. Step 2: Run** all four. **Step 3: Write docs** comparing p50/p99, gaps, and number of `leader-change` events across variants (a small table). **Commit.**

### Task 16: E7–E8 — clock skew and disk (arm64 risk)

**Files:** Create `chaos/E7-clock-skew.yaml`, `chaos/E8a-disk-latency.yaml`, `chaos/E8b-disk-fault.yaml`, `docs/experiments/E7-clock-skew.md`, `docs/experiments/E8-disk.md`; fallback only if needed: `internal/node/faultstore.go` + test, `Dockerfile` faketime stage.

**Interfaces:** E7 `TimeChaos` `timeOffset: "-5m"`, selector leader, `duration: 40s`. E8a `IOChaos` `action: latency`, `delay: 200ms`, `volumePath: /data`, `path: /data/**`, `percent: 100`, selector leader. E8b `IOChaos` `action: fault`, `errno: 5`, `percent: 50`, selector one follower.
Fallback interfaces (only if Step 1 shows the Chaos Mesh action is unsupported):
```go
type FaultLogStore struct { raft.LogStore; Delay time.Duration; FailRate float64; Rand func() float64 } // wraps StoreLog/StoreLogs
```
  Delay/FailRate are swappable at runtime (atomics). kvnode flag `--debug-endpoints` (default false) exposes `POST /debug/store-fault {"delay":"200ms","failRate":0.5}` (zero values = off); `raftctl fault <id> --delay 200ms --fail-rate 0.5` calls it on that node directly. Live, per-pod, no restart. Faketime: image target `faketime` adding libfaketime; E7 fallback sets `FAKETIME=-5m` + `LD_PRELOAD` on one pod. (Note: distroless/static + CGO_ENABLED=0 binaries ignore LD_PRELOAD — if this path is needed, the faketime image must use a dynamically linked build on a debian-slim base; record this in the doc.)

- [ ] **Step 1: Probe support first:** apply each YAML with `duration: 10s` against a kv pod; check `kubectl describe` of the chaos object for injection success, and `kubectl exec` evidence where possible (e.g., `raftctl status` LastContact/latency change). Record outcome.
- [ ] **Step 2 (only if unsupported): implement fallback TDD** — `TestFaultLogStoreDelays` (Delay 50ms → StoreLog takes ≥50ms), `TestFaultLogStoreFails` (FailRate 1, Rand=0 → error; FailRate 0 → passthrough). `TestDebugStoreFaultEndpoint` (in httpapi, only mounted when enabled → 404 otherwise). Wire `--debug-endpoints=true` into `deploy/base`; add `chaos/E8a.sh`/`E8b.sh` that call `raftctl fault` on the target, sleep 40s, then reset to zero.
- [ ] **Step 3: Run** `make exp-E7 exp-E8a exp-E8b`. **Step 4: Write docs**, stating the injection method used. E7 hypothesis explicitly checks "Raft timeouts use monotonic time." **Commit.**

### Task 17: E9 — wiped PVC, and the bootstrap fix

**Files:**
- Create: `chaos/E9a.sh` (wipe follower kv-2), `chaos/E9b.sh` (wipe kv-0), `docs/experiments/E9-wiped-pvc.md`
- Modify: `cmd/kvnode/join.go`, `cmd/kvnode/join_test.go`, `cmd/kvnode/main.go`

**Interfaces:**
- E9 scripts (wipe `kv-N`): record old PVC UID → `kubectl delete pvc data-kv-N --wait=false` (pvc-protection holds it while the pod runs) → `kubectl delete pod kv-N` → wait until `data-kv-N` exists with a **new** UID and is Bound → wait for `kv-N` Ready. (kind's local-path PVs use reclaim `Delete`, so the data is really gone.) Print `raftctl status` + raftwatch events.
- Fix (after E9b demonstrates split brain):
```go
type Prober interface { Statuses(ctx context.Context) []client.StatusResult }
func shouldBootstrap(mode, id string, hasState bool, p Prober, ctx context.Context) (bool, error)
// auto: true only if id ends in "-0" AND !hasState AND no reachable peer (other than self) reports Term > 0.
```

- [ ] **Step 1: Run** `make exp-E9a` → follower rejoins, Lost 0. **Run** `make exp-E9b` → capture the split brain (two leaders, `raftctl status` from each side). This run is **expected** to show loss or divergence — record exactly what happened.
- [ ] **Step 2: Write failing tests:** `TestShouldBootstrapAutoNoPeers` → true; `TestShouldBootstrapAutoPeerHasTerm` → false; `TestShouldBootstrapExistingState` → false; `TestShouldBootstrapNonZeroOrdinal` → false; `TestShouldBootstrapAllPeersUnreachable` → true (fresh cluster cold start).
- [ ] **Step 3: Implement**; main.go probes the `--join` URLs (excluding self) with a 10s overall timeout before deciding; when the decision is "don't bootstrap", run `joinLoop`. **Run** tests → PASS.
- [ ] **Step 4: Rebuild, redeploy, re-run** `make exp-E9b` → kv-0 rejoins the existing cluster, single leader, Lost 0.
- [ ] **Step 5: Write `E9-wiped-pvc.md`** with both runs (before fix / after fix), and the remaining hole: if every peer is unreachable when kv-0 boots wiped, it will still bootstrap. **Commit** `fix(kvnode): don't bootstrap when a cluster already exists`.

### Task 18: E10–E11 — rolling update and membership change

**Files:**
- Create: `chaos/E10.sh`, `chaos/E11a.sh` (scale up), `chaos/E11b.sh` (scale down, naive), `chaos/E11c.sh` (scale down, leave-first), `scripts/scale-down.sh`
- Create: `docs/experiments/E10-rolling-update.md`, `docs/experiments/E11-membership.md`
- Modify: `Makefile` (`scale-down N=`)

**Interfaces:**
- `E10.sh`: `kubectl -n raftlab rollout restart sts/kv && kubectl -n raftlab rollout status sts/kv --timeout=180s`.
- E11 runs on `make cluster-up-5`. `E11a.sh`: scale 3→5, wait Ready, assert 5 voters. `E11b.sh`: scale 5→3 directly; print voters (expect 5 listed, 2 unreachable). `scripts/scale-down.sh N`: for each ordinal ≥ N (highest first): `raftctl leave kv-<i>`, then scale to N. `E11c.sh`: from 5, `scale-down.sh 3`; assert 3 voters.
- Each E11 script must leave the cluster in a known state; `E11b.sh` repairs by running `raftctl leave` for the dead IDs at the end.

- [ ] **Step 1: Run** `make exp-E10` on the 3-worker cluster → Lost 0; count elections.
- [ ] **Step 2: Recreate** with `make cluster-down cluster-up-5 load-image deploy chaos-install smoke`, then run `exp-E11a`, `exp-E11b`, `exp-E11c`.
- [ ] **Step 3: Write docs.** E11 must include the quorum arithmetic in plain words (5 listed voters → need 3; with 2 dead you survive 0 further failures, vs 1 with a clean 3-voter config). **Commit.**

### Task 19: Finish — README, docs index, publish

**Files:** Modify `README.md`, `docs/experiments/README.md`, `docs/glossary.md`

- [ ] **Step 1: README**: what this is (learning project, not production), prerequisites (macOS arm64 tested, Docker ≥ 8 GiB, Go 1.25, kind, helm, kubectl), quickstart (`make tools cluster-up image load-image deploy smoke watch`), journey map (00–04 with one-line summaries), experiment results table (ID, failure, hypothesis held?, lost writes, longest gap), limitations (loadgen is not a linearizability checker; single-machine kind; E7/E8 injection method).
- [ ] **Step 2: Glossary sweep**: every bolded term used in journey/experiment docs has an entry.
- [ ] **Step 3: Full verification from scratch:** `make cluster-down && make test && make cluster-up image load-image deploy chaos-install smoke exp-E1` → all exit 0.
- [ ] **Step 4: Commit, tag `stage-5`, ask before pushing**, then `git push --follow-tags`.
