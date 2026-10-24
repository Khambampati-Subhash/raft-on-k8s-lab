# Experiments

Stage 4's StatefulSet cluster, broken on purpose with [Chaos Mesh](https://chaos-mesh.org/),
under a steady write load. Every experiment answers one question: **does Raft lose an
acknowledged write when this happens?** And on the way: how long are writes unavailable, and
what does the cluster look like while it recovers?

## How to run one

```bash
make cluster-up deploy chaos-install   # once
make exp-E1                            # exits non-zero if loadgen reports Lost > 0
```

[`scripts/exp.sh`](../../scripts/exp.sh) does the same thing every time:

| t | step |
|---|---|
| 0 s | start a loadgen Job (4 writers, 120 s, plus the stale-read probe) and a raftwatch event capture |
| 20 s | label the current leader/followers on the pods, then inject `chaos/<ID>-*.yaml` (or `chaos/<ID>.sh`) |
| 20–60 s | the fault is active (`duration: 40s`) |
| 120 s | writes stop; loadgen reads every acknowledged key back through the leader |
| end | chaos removed, full log written to [`results/`](results/) |

The log has `raftctl status` before and after, the injection record (which pod was hit and its
role at that moment), every raftwatch event, and loadgen's report.

## What the numbers mean

- **ok / failed / indeterminate**: what the client could honestly say about each write
  (stage 2's `internal/client`). `indeterminate` means it may or may not have been applied.
- **Lost**: acknowledged (`ok`) but missing or wrong on read-back. **Must be 0.**
- **unavailability windows**: gaps longer than 500 ms with no successful write.
- **stale-read max lag**: how many probe-counter values behind a node's local copy got.
- **Election time**: measured from raftwatch's own timestamps, from the old leader going
  `unreachable` to the `leader-change`. raftwatch polls every 250 ms, so treat it as ±250 ms.

loadgen detects lost acknowledged writes and stale reads. It is **not** a linearizability
checker.

## Format

Each experiment doc: **Hypothesis** (written before the run) → **Injection** →
**Observed** (real output from `results/`) → **Explanation** → **Takeaway**. If a hypothesis
was wrong, it stays, marked as such.

## Index

| ID | Fault | Hypothesis held? |
|---|---|---|
| [E1](E1-leader-kill.md) | kill the leader pod | Mostly. Lost 0, 1.25 s window; but the killed leader won the next election itself, and nothing was indeterminate |
| [E2](E2-follower-kill.md) | kill a follower pod | Yes. No window, no election, Lost 0 |
| [E3](E3-minority-partition.md) | partition one follower away | Yes. No window, no term inflation on heal (pre-vote), isolated follower 397 behind on stale reads |
| [E4](E4-isolate-leader.md) | partition the leader away from both followers | Yes. Old leader stepped down in 0.68 s, 2.16 s window, exactly 4 indeterminate (all absent), Lost 0 |
| [E5](E5-latency.md) | 100 ms / 800 ms latency between nodes | 100 ms yes. **800 ms no**: RTT above the 500 ms leader lease means no leader survives; 40 s of zero writes, Lost 0 |
| [E6](E6-packet-loss.md) | 10 % / 40 % packet loss between nodes | Yes. 10 %: no elections. 40 %: 10 leader changes, 14 windows, Lost 0 |
| [E7](E7-clock-skew.md) | leader clock −5 min | **Not run.** TimeChaos segfaults Go's runtime on arm64. Code reading of hashicorp/raft says the timers are monotonic |
| [E8](E8-disk.md) | slow / failing log appends (in-process injector) | Partly. Lost 0 and no elections, but **E8b found a replication deadlock in hashicorp/raft v1.8.0**: the faulty follower stops replicating for good while looking healthy (2/2 runs) |
