# E1 — Kill the leader

## Hypothesis

_Written before the run._

- Writes stop for **1–3 s**: about one heartbeat timeout (1 s default) for a follower to give
  up on the dead leader, plus a vote round. loadgen shows **exactly one** unavailability
  window of roughly that size.
- A follower wins a new term (**term +1**). The killed pod comes back under the **same name**
  with its PVC and rejoins as a **follower**. The membership stays exactly `kv-0..kv-2`.
- Writes in flight on the leader when it died come back **indeterminate** (a handful at
  most). Writes sent while there's no leader are **failed** (503/refused).
- **Lost: 0.**

## Injection

[`chaos/E1-leader-kill.yaml`](../../chaos/E1-leader-kill.yaml): a Chaos Mesh `PodChaos` with
`action: pod-kill`, `mode: one`, selecting the pod `exp.sh` labelled `raft-role=leader`
immediately before. Fresh cluster, 4 loadgen writers, kill at t≈20 s.

```
leader before injection: kv-0
  kv-0 Running leader l
  kv-1 Running follower f1
  kv-2 Running follower f2
injected at 2026-10-24T08:45:07.798+00:00
chaos record: e1-leader-kill raftlab/kv-0 phase=Injected injected=1
```

## Observed

From [`results/E1-20261024t084423.log`](results/E1-20261024t084423.log):

```
## raftwatch events
08:45:07.978 unreachable   kv-0
08:45:08.727 reachable     kv-0   role=follower term=2
08:45:09.976 leader-change kv-0   - -> kv-0
08:45:09.976 term          kv-0   2 -> 3
08:45:09.976 role          kv-0   follower -> leader
08:45:09.976 term          kv-1   2 -> 3
08:45:09.976 term          kv-2   2 -> 3

## loadgen
  writes: 283831 total, 283639 ok, 192 failed, 0 indeterminate (0 present, 0 absent)
  latency (ok): p50 1.605ms, p99 3.454ms
  unavailability windows: 1
    08:45:08.608 .. 08:45:09.856 (1.249s)
  stale-read max lag: kv-0=208 kv-1=0 kv-2=0
  Lost: 0
```

Membership after the run: exactly `kv-0`, `kv-1`, `kv-2`, all at term 3, same commit index.

| Prediction | Held? |
|---|---|
| Writes stop for 1–3 s, exactly one window | **Yes.** One window, 1.25 s. From the old leader going unreachable to the new leader showing up in raftwatch: 2.0 s (±0.25 s raftwatch polling). |
| A follower wins term +1 | **Term +1 yes. "A follower wins": no.** The *killed* pod won. |
| Killed pod returns under the same name and rejoins as follower | **Same name and PVC, yes.** It came back in 0.75 s and then became leader again. |
| A handful of indeterminate writes, the rest of the outage failed | **No.** 0 indeterminate, 192 failed. |
| Lost: 0 | **Yes.** |

## Explanation

**Why the dead leader won its own election.** The replacement `kv-0` was reachable 0.75 s
after the kill: same name, same DNS address, same disk, so the same log. That's faster than
the followers' heartbeat timeout (1 s), so for a moment nobody had started an election yet.
The new `kv-0` process starts as a follower, hears from no leader (it *was* the leader), and
its own timer fires first. Its log is as up to date as anyone's, so the others vote for it.
Raft doesn't care who leads, only that the leader's log contains every committed entry. A
StatefulSet that brings members back this fast makes "the old leader returns and wins" a
normal outcome, not an edge case.

**Why nothing was indeterminate.** Chaos Mesh's pod-kill deletes the pod with a 0-second
grace period, and Kubernetes still delivers SIGTERM first. Our reading of the timeline (the
old pod's logs are gone with it): `kvnode` stopped accepting new connections at once
(raftwatch: `unreachable` at 07.978), but requests already in flight on open connections
completed. Acknowledged writes continued until 08.608. After that, every new write was
refused at TCP connect, which is a definite "not applied" (stage 2's `Classify`: a dial error
is `failed`). No request was cut off after being sent, so none were indeterminate. A harder
kill (`kill -9`, a node crash) would cut requests in flight. Stage 2's local `kill -9` run
produced 4 indeterminate writes.

**Why the window (1.25 s) is shorter than the election (2.0 s).** The window starts at the
last *acknowledged* write (08.608, still being served by the dying leader) and ends at the
first write the new leader acknowledged (09.856). raftwatch only polls every 250 ms, so its
`leader-change` at 09.976 is up to 250 ms late.

**Stale lag 208 on `kv-0`.** The probe counter was at about 208 when `kv-0` died. A lag equal
to the *whole* counter means the restarted `kv-0` answered a stale read with "not found". A
restarted member has its log on disk but doesn't know how much of it is committed until a
leader tells it, so it applies nothing in the meantime. For a moment its local map was
**empty**: further in the past than anything it had served before the restart. Stale reads
aren't only "a bit behind". On a restarted node they can go back in time.

### Unplanned: the first E1 run OOM-killed the new leader

[`results/E1-20261023t221336-oom.log`](results/E1-20261023t221336-oom.log) also has Lost 0.
But it holds a second, unplanned election: two minutes after the injection, during loadgen's
verify phase, the new leader `kv-2` was `OOMKilled` (exit 137) at the then-limit of 256 Mi.
Its last log lines:

```
22:15:53 raft: starting snapshot up to: index=276719
22:15:53 raft: compacting logs: from=12163 to=266479
22:15:55 raft: snapshot complete up to: index=276719
```

and five seconds later the kernel killed it. Three things added up:

- The cluster had about 300k keys (that run did not yet start from fresh data). The leader
  sat at 176 Mi just idling.
- `SnapshotThreshold` is 1024 entries, but hashicorp/raft only *checks* it every
  `SnapshotInterval` (120 s). At ~2k writes/s, about 250k entries pile up between snapshots.
- A snapshot JSON-encodes the whole map in memory, and compacting 254k log entries is one
  large bolt write transaction.

Fixes, in [`deploy/base/statefulset.yaml`](../../deploy/base/statefulset.yaml) and
[`scripts/exp.sh`](../../scripts/exp.sh): memory limit 512 Mi with `GOMEMLIMIT=460MiB` (so
Go's GC tightens up before the kernel kills the process), every experiment starts from empty
volumes, and `exp.sh` now prints `WARN` for any restart the chaos didn't cause, since this run
had said `PASS`. As a side result, verify got through an unplanned leader failover with
0 verify errors.

Two later runs, [`E1-20261023t222005-suspended.log`](results/E1-20261023t222005-suspended.log)
and E2's, are kept but not used: the laptop went to sleep mid-run and froze the Docker VM,
so their timings are meaningless (`exp.sh` now detects this).

## Takeaway

Killing the leader cost **1.25 s of writes and zero acknowledged writes**. The interesting
part is who came back: with a StatefulSet, the killed leader returns with its identity and
log so fast that it can win the next election itself. And a just-restarted member's stale
reads can briefly show an empty store. If you serve stale reads, don't serve them from a node
that hasn't heard from a leader since it started.
