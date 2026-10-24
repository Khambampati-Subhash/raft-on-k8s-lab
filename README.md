# raft-on-k8s-lab

> **Learning project — not production software.**

A hands-on lab for seeing how Raft consensus behaves when real-world failures hit a
Kubernetes cluster. It imports [`hashicorp/raft`](https://github.com/hashicorp/raft) (v1.8.0),
wraps it in a tiny key-value service, runs it on `kind`, and breaks it on purpose with
[Chaos Mesh](https://chaos-mesh.org/). Every claim in the docs is backed by output pasted from
a real run, and every hypothesis that turned out wrong is kept and marked as wrong.

## What's here

```
cmd/kvnode      one Raft member: HTTP API on :8080, Raft on :7000
cmd/loadgen     write workload + checker: did any acknowledged write go missing?
cmd/raftwatch   live view of every node's role/term/leader, and a stream of events
cmd/raftctl     leader | status | leave <id> | fault <id>
cmd/naivekv     stages 0–1: the "before Raft" key-value server
internal/       fsm (the state machine), node (Raft wrapper), httpapi, client (leader-following)
deploy/         stage 3 (Deployment, wrong on purpose) and base/ (StatefulSet, stage 4+)
chaos/          one file per experiment, run by scripts/exp.sh
docs/           the journey, the experiments, and a glossary
```

## Prerequisites

Tested on **macOS on Apple Silicon (arm64)** with Docker Desktop. You need:

- Docker with **≥ 8 GB** of memory (`make tools` checks for 7.5 GiB, what "8 GB" reports)
- Go 1.25
- `kind`, `kubectl`, `helm`

## Quickstart

```bash
make test                                   # unit tests
make tools cluster-up deploy smoke          # 3-worker kind cluster, StatefulSet, smoke test
make watch                                  # live raftwatch (Ctrl-C to quit)
make chaos-install exp-E1                   # Chaos Mesh, then the first experiment
make cluster-down
```

`make deploy` builds the image (cross-compiled on the host) and loads it into kind first.
E11 needs 5 workers: `make cluster-down cluster-up-5 chaos-install deploy`.

## The journey

Read these in order. Each stage fixes what broke in the one before it.

| Stage | | What you learn |
|---|---|---|
| [0](docs/journey/00-single-node.md) | One server, no copies | "Saved" is a promise one in-memory server can't keep past a crash. |
| [1](docs/journey/01-naive-replication.md) | Three copies, no agreement | Copies drift apart without an agreed order of writes, and nothing errors. |
| [2](docs/journey/02-raft-local.md) | Raft on one laptop | One leader, one log, majority commits: kill the leader and nothing acknowledged is lost. |
| [3](docs/journey/03-raft-on-deployment.md) | Raft on a Deployment (wrong) | New pod names, IPs and empty disks leave ghost voters, wedge the cluster, and bootstrap an empty twin. |
| [4](docs/journey/04-raft-on-statefulset.md) | Raft on a StatefulSet | Stable identity, address and disk. Plus the two probe traps: `OrderedReady` and liveness on `/readyz`. |

## The experiments

Each one: a fresh 3-member cluster, 4 writers for 120 s, the fault at t=20 s for 40 s, then
every acknowledged write read back through the leader. Details and raw logs in
[`docs/experiments/`](docs/experiments/README.md).

| ID | Failure | Hypothesis held? | Lost acknowledged writes | Longest window without writes |
|---|---|---|---|---|
| [E1](docs/experiments/E1-leader-kill.md) | kill the leader | mostly (the killed leader won the next election) | 0 | 1.25 s |
| [E2](docs/experiments/E2-follower-kill.md) | kill a follower | yes | 0 | none |
| [E3](docs/experiments/E3-minority-partition.md) | partition one follower | yes | 0 | none |
| [E4](docs/experiments/E4-isolate-leader.md) | partition the leader from both followers | yes | 0 | 2.16 s |
| [E5](docs/experiments/E5-latency.md) | 100 ms / 800 ms network latency | 100 ms yes; **800 ms no** | 0 / 0 | none / **40.3 s** |
| [E6](docs/experiments/E6-packet-loss.md) | 10 % / 40 % packet loss | yes | 0 / 0 | 0.59 s / 10.0 s |
| [E7](docs/experiments/E7-clock-skew.md) | leader clock −5 min | **not run** (tool crashes Go on arm64) | — | — |
| [E8](docs/experiments/E8-disk.md) | slow / failing log appends | partly: **follower replication deadlock** | 0 / 0 | 0.62 s / none |
| [E9](docs/experiments/E9-wiped-pvc.md) | a node's disk wiped | follower yes; `kv-0` **no** | **170,972** before the bootstrap fix, **75** after | 51.1 s |
| [E10](docs/experiments/E10-rolling-update.md) | rolling restart | mostly | 0 | 5.13 s |
| [E11](docs/experiments/E11-membership.md) | scale 3→5, 5→3 naive, 5→3 leave-first | yes | 0 | none |

The short version:

- **Raft held its promise every time its storage did.** Leader kills, partitions, latency,
  packet loss, rolling restarts and membership changes all ended with **0 lost acknowledged
  writes**. Failures cost availability, never correctness.
- **When a disk was wiped, it didn't.** A node that loses its log can bootstrap a second
  cluster (fixed: `kvnode` now asks its peers first) or vote in a leader that's missing
  committed entries (not fixed: needs rejoin-as-non-voter).
- **The quiet failures are the dangerous ones.** A follower deadlocked out of replication
  (E8b, [hashicorp/raft#612](https://github.com/hashicorp/raft/issues/612)) and a naive
  scale-down that left no failure tolerance (E11b) both looked perfectly healthy. Watch
  per-follower replication lag and the voter list, not just liveness.

## Limitations

- **loadgen is not a linearizability checker.** It detects acknowledged writes that go
  missing or wrong, and stale reads. It doesn't check that reads and writes are ordered
  correctly with respect to each other.
- **Single machine.** All of kind runs in one Docker VM on one laptop: "nodes" share a kernel,
  a clock and a disk, and latency between them is near zero unless injected. If the laptop
  sleeps, every run's timings are invalid (`exp.sh` detects and flags this).
- **E7 and E8 don't use Chaos Mesh.** On arm64, TimeChaos crashes Go programs and IOChaos
  can't start (it ships an x86 helper). E7 is answered by reading hashicorp/raft's timer code.
  E8 uses an in-process fault injector on the Raft log store only
  (`kvnode --debug-endpoints`, `raftctl fault`), not a real slow or failing disk.
- **`--debug-endpoints=true` is on in `deploy/base`** so E8 can run. It exposes an
  unauthenticated fault-injection endpoint. Turn it off anywhere that isn't this lab.
- No TLS, no auth, no metrics stack.

Words in bold throughout the docs are defined in the [glossary](docs/glossary.md).
