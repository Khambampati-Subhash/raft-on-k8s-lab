# E2 — Kill a follower

## Hypothesis

_Written before the run._

- **No leader change, no term change.** 2 of 3 is still a majority, and the leader keeps
  committing with the other follower.
- **No unavailability window** over 500 ms, and no failed or indeterminate writes. Clients only
  talk to the leader, which never stopped.
- The killed follower comes back under the same name with its PVC, and the leader sends it
  the entries it missed. loadgen only reports the **max** stale-read lag per node, so the
  prediction is: the killed follower's max lag is large (it fell behind while down), and the
  surviving follower's stays small (a few counter values at most).
- **Lost: 0.**

## Injection

[`chaos/E2-follower-kill.yaml`](../../chaos/E2-follower-kill.yaml): same as E1, selecting
`raft-role=follower` (`mode: one` picks one of the two).

```
leader before injection: kv-0
injected at 2026-10-24T08:48:53.125+00:00
chaos record: e2-follower-kill raftlab/kv-2 phase=Injected injected=1
```

## Observed

From [`results/E2-20261024t084805.log`](results/E2-20261024t084805.log):

```
## raftwatch events
08:48:53.293 unreachable   kv-2
08:48:54.294 reachable     kv-2   role=follower term=2

## loadgen
  writes: 312124 total, 312124 ok, 0 failed, 0 indeterminate (0 present, 0 absent)
  latency (ok): p50 1.509ms, p99 2.777ms
  unavailability windows: 0
  stale-read max lag: kv-0=0 kv-1=0 kv-2=208
  Lost: 0
```

That's the entire raftwatch event stream: no `leader-change`, no `term` event. After the
run, all three commit indexes match (`raftctl status`): `kv-0=313330 kv-1=313330 kv-2=313330`.
(Checked after [E8](E8-disk.md) showed a follower can look healthy while not replicating.)

| Prediction | Held? |
|---|---|
| No leader change, no term change | **Yes.** Term stayed 2. |
| No window > 500 ms, no failed/indeterminate writes | **Yes.** 312,124 of 312,124 ok. |
| Killed follower returns with its PVC and catches up | **Yes.** Back in 1 s as `kv-2`, follower, term 2. |
| Killed follower's max lag large, surviving follower's small | **Yes:** 208 vs 0. |
| Lost: 0 | **Yes.** |

## Explanation

Clients only ever write to the leader, and the leader needs 2 of 3 to commit: itself plus the
surviving follower. Losing `kv-2` removed a spare, not a requirement, so the write path never
noticed. p99 latency even went *down* compared with E1 (2.8 ms vs 3.5 ms). There were fewer
followers to replicate to for a second, and no election.

The 208 lag on `kv-2` is the same effect as in [E1](E1-leader-kill.md#explanation): the
restarted member briefly answered stale reads from an empty map before the leader told it
the commit index. `kv-1`'s lag of 0 means the surviving follower was never caught more than
a probe tick behind.

## Takeaway

A 3-node cluster shrugs off a follower dying: **no unavailability at all**. That is the
"tolerates 1 failure" promise working as advertised. The cost is invisible until the next
failure: for the second or so `kv-2` was gone, losing either remaining node would have
stopped all writes.
