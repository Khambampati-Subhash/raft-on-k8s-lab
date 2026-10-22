# raft-on-k8s-lab — Design Spec

- **Date:** 2026-10-22
- **Status:** Approved design, pending spec review
- **Repo:** `github.com/Khambampati-Subhash/raft-on-k8s-lab` (public, learning project)

## 1. Purpose

A hands-on lab for learning how Raft consensus behaves when real-world failures hit a
Kubernetes-hosted cluster. We do **not** implement Raft — we import `hashicorp/raft` and
build a small key-value (KV) service around it, deploy it on a local `kind` cluster, and
break it on purpose: kill pods, partition the network, slow the disk, skew clocks, wipe
data, roll deploys, change membership under load.

The second deliverable is the **write-up**: every stage is documented in plain English,
starting from the naive version, showing (with real output) how it breaks, why, and what
fixed it.

### Success criteria

1. One command brings up a 3-node Raft KV cluster on kind; one command tears it down.
2. Every chaos experiment in §7 has a runnable `make` target and a doc with hypothesis,
   real observed output, and explanation.
3. Under every experiment, the loadgen checker reports **0 lost acknowledged writes**
   (or, where it is non-zero, the doc explains the bug we found and the fix).
4. A reader with basic programming knowledge but no distributed-systems background can
   follow `docs/journey/` from stage 0 to the end.

### Audience

The author and other learners. Clarity beats production polish.

## 2. Scope

**In:** Go KV service on `hashicorp/raft`, kind cluster, StatefulSet deployment, Chaos Mesh
fault injection, loadgen/checker, terminal watcher, journey + experiment docs.

**Out (YAGNI):** TLS, authentication, sharding / multi-Raft, cloud deployment, Prometheus /
Grafana, Helm chart for our own app (Helm is used only to install Chaos Mesh), a full
linearizability checker (Jepsen / Porcupine).

## 3. Environment

- macOS, Apple Silicon (arm64), 16 cores, 48 GB RAM.
- Go 1.25, Docker, kubectl 1.34, gh — present.
- `kind`, `helm` — to install via Homebrew.
- kind cluster: 1 control-plane + 3 workers (5 workers for the scaling experiment).
- All images built for `linux/arm64` and loaded with `kind load docker-image` (no registry).

## 4. Architecture

```
kind cluster
├── namespace raftlab
│   ├── StatefulSet "kv" (3 replicas, podAntiAffinity → one pod per worker node)
│   │   └── container kvnode
│   │       ├── :8080 HTTP API
│   │       ├── :7000 Raft TCP transport
│   │       └── /data  (PVC) → raft-boltdb log+stable store, file snapshot store
│   ├── Service "kv" (headless)  → kv-0.kv.raftlab.svc.cluster.local, ...
│   ├── Service "kv-client"      → any pod, for loadgen
│   ├── PodDisruptionBudget      → maxUnavailable: 1
│   ├── Deployment "raftwatch"   → terminal view of every node's /status
│   └── Job "loadgen"            → write/verify workload
└── namespace chaos-mesh          → Chaos Mesh (Helm, containerd runtime)
```

### Key Kubernetes decisions (each one is a journey lesson)

| Decision | Why |
|---|---|
| StatefulSet, not Deployment | Stable pod names → stable Raft server IDs and addresses. Stage 3 shows the Deployment failure. |
| Headless Service | Stable per-pod DNS for the Raft transport; pod IPs change on restart. |
| PVC per pod | Raft log and term must survive restarts, or the node "forgets" votes and committed entries. |
| `podManagementPolicy: Parallel` | With `OrderedReady` + a readiness probe that needs a leader, kv-1 waits for kv-0 to be ready, and kv-0 can't be ready without a quorum → deadlock. |
| Liveness = process alive only | Tying liveness to "has a leader" makes kubelet restart every node during an election → cascading failure. |
| Readiness = knows a leader | Routes client traffic away from nodes that can't serve. |
| PDB maxUnavailable 1 | Voluntary disruptions (drains, rollouts) never take out quorum. |
| podAntiAffinity (required) | A node failure takes out exactly one Raft member — the realistic failure domain. |

## 5. Components

Module: `github.com/Khambampati-Subhash/raft-on-k8s-lab`

```
cmd/
  naivekv/     stage 0–1 naive server (no Raft), kept for the journey
  kvnode/      the real Raft KV node (wiring only)
  loadgen/     workload + checker
  raftwatch/   live cluster status view
internal/
  fsm/         KV state machine
  node/        Raft wrapper
  httpapi/     HTTP handlers
  client/      shared HTTP client (leader-following) used by loadgen & raftwatch
deploy/
  kind/        kind cluster configs (3-worker, 5-worker)
  base/        kustomize base (StatefulSet, Services, PDB, raftwatch)
  stage3-deployment/  the deliberately wrong Deployment variant
chaos/         Chaos Mesh experiment YAMLs, one per experiment
docs/
  journey/     NN-<stage>.md
  experiments/ <experiment>.md
  glossary.md
Makefile
```

### 5.1 `internal/fsm` — state machine

- Implements `raft.FSM`: `Apply(*raft.Log) any`, `Snapshot() (raft.FSMSnapshot, error)`,
  `Restore(io.ReadCloser) error`.
- State: `map[string]string` guarded by `sync.RWMutex`.
- Commands: JSON `{"op":"set"|"delete","key":"...","value":"..."}`.
- Snapshot: JSON-encodes a copy of the map; Restore replaces the map wholesale.
- No network, no Raft imports beyond the interface types. Fully unit-testable.

### 5.2 `internal/node` — Raft wrapper

Exposes an interface consumed by `httpapi`:

```go
type Store interface {
    Set(ctx context.Context, key, value string) error
    Delete(ctx context.Context, key string) error
    Get(ctx context.Context, key string, stale bool) (string, bool, error)
    Join(id, addr string) error
    Leave(id string) error
    Status() Status   // id, role, term, leader id/addr, commit/applied/last index, last contact, voters
}
```

- Errors are typed: `ErrNotLeader{LeaderAddr}`, `ErrTimeout` (outcome unknown), `ErrNoLeader`.
- **Writes:** `raft.Apply` with a timeout; non-leader returns `ErrNotLeader`.
- **Reads:** default linearizable — `VerifyLeader()` then read local FSM (leader only);
  `stale=true` reads local FSM on any node with no checks.
- **Storage:** `raft-boltdb/v2` for log + stable store, `raft.NewFileSnapshotStore` in
  `/data/snapshots`, retain 2.
- **Identity:** server ID = pod name (`kv-0`); advertise address = `kv-0.kv.raftlab.svc.cluster.local:7000`.
- **Bootstrap (initial, naive on purpose):** if no existing Raft state and ordinal == 0,
  bootstrap a single-node cluster; ordinals > 0 call `POST /join` on the seed address
  with retry/backoff, following `421` leader hints. The "wiped kv-0" experiment (§7, E9)
  exposes the split-brain hazard in this rule; the fix (check peers for an existing
  cluster before bootstrapping) lands as part of that experiment.
- **Config:** heartbeat / election / leader-lease timeouts and `SnapshotThreshold` exposed
  as flags. Defaults = hashicorp/raft defaults, except `SnapshotThreshold` = 1024 so
  snapshots actually happen during experiments.

### 5.3 `internal/httpapi`

| Method / path | Behavior |
|---|---|
| `PUT /kv/{key}` (body = value) | 204 on commit. 421 + `X-Raft-Leader` header if not leader. 503 if no leader. 504 `outcome-unknown` on apply timeout. |
| `DELETE /kv/{key}` | Same codes as PUT. |
| `GET /kv/{key}` | Linearizable read; 200 / 404 / 421 / 503. |
| `GET /kv/{key}?stale=true` | Local read on any node; 200 / 404. |
| `GET /status` | JSON `Status`. |
| `POST /join` `{"id","addr"}` | Leader adds voter; 421 if not leader. |
| `POST /leave` `{"id"}` | Leader removes server; 421 if not leader. |
| `GET /healthz` | 200 if process is up. |
| `GET /readyz` | 200 if node knows a leader, else 503. |

Handlers depend only on the `Store` interface (tests use a fake).

### 5.4 `cmd/loadgen` — workload and checker

- **Write phase:** N workers (default 4) for duration D (default 60s). Key =
  `<runID>/<worker>/<seq>`, value = random 16 bytes hex. Each write's outcome is classified:
  - `ok` — 2xx → recorded as **acknowledged**.
  - `failed` — definite non-commit: 421/503, or connection refused before the request was sent.
  - `indeterminate` — timeout, 504, connection reset after send. May or may not have committed.
  - Client follows `X-Raft-Leader` hints (max 3 hops).
- **Verify phase:** after the write phase (and after chaos is removed), read every
  acknowledged key linearizably from the leader. **Lost** = acknowledged but missing or
  wrong value. Indeterminate keys are reported as "present" / "absent" for info only.
- **Stale-read probe (optional flag):** writes a monotonically increasing counter via the
  leader and reads it `stale=true` from every pod; reports max observed lag per pod.
- **Report:** human summary + JSON file: totals per class, **lost count (must be 0)**,
  p50/p99 write latency, unavailability windows (gaps > 500 ms with no successful write).
- Exit code non-zero if lost > 0.
- Limitation stated in docs: detects lost acknowledged writes and stale reads; it is not a
  linearizability checker.

### 5.5 `cmd/raftwatch`

- Polls `/status` of every pod (by headless DNS) every 250 ms.
- Redraws a table: node, role, term, leader, commit idx, applied idx, last contact, reachable.
- Prints timestamped event lines on change: leader change, term bump, node unreachable/back.
- Runs in-cluster as a Deployment; viewed with `make watch` (`kubectl exec -it`).
- Not targeted by chaos selectors. Docs note that its view can itself be affected by
  partitions it isn't in.

### 5.6 `cmd/naivekv` (stages 0–1 only)

- Single-file in-memory KV over HTTP. `--peers` flag enables best-effort fire-and-forget
  replication to peers. No consensus, no catch-up. Exists only to be broken in the journey.

## 6. Journey stages

Each stage = a git tag `stage-N` + `docs/journey/0N-<name>.md`.

| # | Name | Naive version | What breaks (demonstrated with real output) | Fix → next stage |
|---|---|---|---|---|
| 0 | single-node | `naivekv`, one process | Kill it → all data gone; it's also down | Need copies |
| 1 | naive-replication | `naivekv --peers`, 3 local processes | Kill a peer, write, restart it → it's missing data forever; two clients writing the same key on different nodes → nodes end with different values | Need agreement on order → consensus |
| 2 | raft-local | `kvnode` × 3 local processes | It works: leader kill → re-election, no lost writes. Explains terms, log, quorum | Move to Kubernetes |
| 3 | raft-on-deployment | `deploy/stage3-deployment` (Deployment, pod IPs, emptyDir) | Restart pods → new IPs, Raft config points at dead addresses, data gone, quorum never returns | Stable identity + storage |
| 4 | raft-on-statefulset | `deploy/base` | Works; walks through every decision in §4's table (incl. the OrderedReady deadlock and liveness traps) | Start breaking it |
| 5+ | chaos | experiments in §7 | — | — |

## 7. Chaos experiments

Each experiment: `chaos/<id>-<name>.yaml` (or a script where Chaos Mesh doesn't fit),
`make exp-<id>`, and `docs/experiments/<id>-<name>.md` with
**Hypothesis → Injection → Observed (raftwatch events + loadgen report) → Explanation → Takeaway.**
Loadgen runs during every experiment.

| ID | Experiment | Tool | Hypothesis (to be confirmed or refuted) |
|---|---|---|---|
| E1 | Leader pod kill | PodChaos `pod-kill` (target: current leader via label set by script) | New leader within ~2× election timeout; brief unavailability; 0 lost writes. |
| E2 | Follower pod kill | PodChaos | No unavailability; follower rejoins and catches up from log/snapshot. |
| E3 | Majority/minority partition | NetworkChaos `partition` (1 vs 2) | Majority side keeps serving; minority can't commit; stale reads on minority return old data; heals cleanly. |
| E4 | Isolated old leader | NetworkChaos isolating the leader | Old leader steps down after lease timeout; new leader elected on majority side; writes to old leader fail/time out, never ack. 0 lost writes. |
| E5 | Latency | NetworkChaos `delay` (e.g., 100 ms ± 50, then 800 ms) | Moderate latency raises write latency only; latency near election timeout causes leadership flapping. |
| E6 | Packet loss | NetworkChaos `loss` (10%, 40%) | Retries mask low loss; high loss causes elections and indeterminate writes. |
| E7 | Clock skew | TimeChaos (±5 min on one pod) | Minimal effect: Raft timeouts use monotonic time, not wall clock. |
| E8 | Slow / failing disk | IOChaos `latency` and `fault` on `/data` | Slow fsync on leader slows all commits; errors on a follower's disk are survivable; on the leader they force stepdown or crash. |
| E9 | Wiped PVC | script: scale down, delete PVC, scale up | Wiped follower rejoins and catches up. **Wiped kv-0 re-bootstraps a new 1-node cluster → split brain** (expected to expose the naive bootstrap rule in §5.2; fix and re-run). |
| E10 | Rolling update | `kubectl rollout restart` | PDB keeps quorum; at most a couple of elections; 0 lost writes. |
| E11 | Membership change | script: kind 5-worker cluster, scale 3→5 (join), 5→3 (explicit `/leave` first, then scale) | Joins under load are safe. Scaling down **without** `/leave` leaves dead voters in the config and shrinks fault tolerance — shown, then fixed by the leave-first procedure. |

Chaos Mesh on kind needs `chaosDaemon.runtime=containerd` and
`chaosDaemon.socketPath=/run/containerd/containerd.sock`.

## 8. Error handling principles

- Never report success unless Raft committed the entry.
- A timeout is **outcome-unknown**, never "failed". This distinction is surfaced in the API
  (504 `outcome-unknown`) and in loadgen's classification.
- Non-leaders redirect (421 + leader address) rather than proxy; following the redirect is
  the client's job. Keeps the server simple and makes leader changes visible.
- Startup failures (can't open bolt DB, can't bind port) exit non-zero so Kubernetes
  restarts the pod; join failures retry with capped exponential backoff.
- Graceful shutdown on SIGTERM: stop HTTP, `raft.Shutdown()`, close stores.

## 9. Testing

| Level | What | How |
|---|---|---|
| Unit | `fsm` Apply / Snapshot / Restore round-trip, bad commands | `go test`, pure |
| Unit | `httpapi` status codes for every error type | fake `Store` |
| Unit | loadgen outcome classification | table tests with `httptest` |
| Integration | 3 in-process nodes on `raft.InmemTransport`: election, replication, leader failover, join, leave, snapshot+restore of a lagging node | `go test ./internal/node -run Integration`, seconds, no k8s |
| Cluster smoke | `make cluster-up deploy smoke`: 3 ready pods, one leader, write+read round-trip | Makefile script |
| Experiments | each `make exp-<id>` exits non-zero if loadgen reports lost > 0 | Makefile + loadgen exit code |

TDD for `fsm`, `httpapi`, `node`, and loadgen classification.

## 10. Documentation conventions

- Plain English, short sentences. Every term is explained the first time it appears and
  collected in `docs/glossary.md` (term, leader, follower, candidate, quorum, log, commit,
  snapshot, linearizable, stale read, partition, split brain, ...).
- Every "it broke" claim is backed by **real pasted output** (raftwatch events, loadgen
  report, `kubectl` output). No invented output.
- Each journey doc: *What we tried → What broke → Why → What we changed → What we learned.*
- Each experiment doc: *Hypothesis → Injection → Observed → Explanation → Takeaway*, and
  whether the hypothesis held. Refuted hypotheses are kept, not rewritten.
- README: what this is (learning project, not production), prerequisites, quickstart, map of
  docs, limitations.

## 11. Makefile targets

`tools` (brew install kind helm check), `test`, `build`, `image`, `cluster-up`,
`cluster-up-5`, `cluster-down`, `load-image`, `deploy`, `undeploy`, `chaos-install`,
`smoke`, `watch`, `load`, `exp-E1` … `exp-E11`, `stage3-deploy`.

## 12. Risks and open items

| Risk | Mitigation |
|---|---|
| TimeChaos / IOChaos behavior on arm64 kind nodes may not work | Verify both on day one of the chaos stage. Fallbacks: E7 via a `kvnode-faketime` image variant using libfaketime (`FAKETIME` env); E8 via a `--debug-store-delay` / `--debug-store-fail-rate` flag that wraps the Raft log store. The experiment doc states which method was used. |
| IOChaos needs the exact volume mount path; kind's local-path provisioner uses hostPath | Target the container mount `/data`; verify in E8 setup. |
| Targeting "the current leader" with Chaos Mesh selectors | Script queries `/status`, labels the leader pod `raft-role=leader`, experiment selects on that label. |
| Docker Desktop resource limits too low for 5 kind nodes + Chaos Mesh | Check Docker Desktop memory allocation (≥ 8 GB) in `make tools`. |
| hashicorp/raft API details (e.g., `VerifyLeader` cost, bootstrap semantics) differ from assumptions | Integration tests in stage 2 pin behavior before k8s work begins. |

## 13. Public repo

Created at implementation start (with explicit confirmation), MIT license, README states
"learning project — not production software."
