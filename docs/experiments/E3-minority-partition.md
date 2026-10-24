# E3 — Partition one follower away

## Hypothesis

_Written before the run._

- The majority side (leader + one follower) keeps committing: **no leader change, no window
  over 500 ms, Lost 0**. Writes never touch the cut-off follower.
- The isolated follower stops hearing heartbeats. After ~1 s its election timer fires, but
  **pre-vote** fails (it can reach nobody), so its **term does not increase** during the 40 s.
- Its local map freezes, so its **stale-read lag grows** for the whole 40 s, to roughly
  40 s × 10 probe writes/s ≈ **400**. raftwatch can still reach it (only kv↔kv traffic is cut).
- When the partition heals, it gets the missed entries from the leader and rejoins **without
  disturbing the leader**: no `leader-change`, no term bump. Without pre-vote, it would come
  back with an inflated term and force an election.

## Injection

[`chaos/E3-minority-partition.yaml`](../../chaos/E3-minority-partition.yaml): a
`NetworkChaos` `partition`, `direction: both`, between `raft-rank=f1` (one follower) and every
other kv pod. loadgen and raftwatch run in other pods and still reach all three nodes.

```
leader before injection: kv-0
  kv-0 Running leader l
  kv-1 Running follower f1
  kv-2 Running follower f2
injected at 2026-10-24T08:53:56.912+00:00
chaos record: e3-minority-partition raftlab/kv-0 phase=Not Injected injected=1
chaos record: e3-minority-partition raftlab/kv-2 phase=Not Injected injected=1
chaos record: e3-minority-partition raftlab/kv-1 phase=Not Injected injected=1
```

(`exp.sh` reads the records after the 40 s are over, so `phase` already shows the recovered
state. `injected=1` is the evidence that the rules were applied.)

## Observed

From [`results/E3-20261024t085310.log`](results/E3-20261024t085310.log), the complete
raftwatch event stream:

```
08:53:59.594 role          kv-1   follower -> candidate
08:54:37.344 role          kv-1   candidate -> follower
```

```
  writes: 329953 total, 329953 ok, 0 failed, 0 indeterminate (0 present, 0 absent)
  latency (ok): p50 1.415ms, p99 2.685ms
  unavailability windows: 0
  stale-read max lag: kv-0=0 kv-1=397 kv-2=0
  Lost: 0
```

| Prediction | Held? |
|---|---|
| Majority keeps committing: no leader change, no window, Lost 0 | **Yes.** Every one of 329,953 writes ok. |
| Isolated follower's election timer fires, but its term doesn't increase | **Yes.** `kv-1` became a candidate 2.7 s after the cut. There is no `term` event for it anywhere in the run. |
| Its stale-read lag grows to ~400 | **Yes: 397.** |
| On heal it rejoins without disturbing the leader | **Yes.** `candidate -> follower` at 40.4 s, no `leader-change`, no term change. |
| …and gets the missed entries | **Yes.** After the run all commit indexes match: `kv-0=331158 kv-1=331158 kv-2=331158`. |

## Explanation

The leader needs a majority, 2 of 3, and it still had one: itself and `kv-2`. For the write
path, `kv-1` simply didn't exist for 40 s.

`kv-1`'s view is the interesting part. After ~1 s without heartbeats it gave up on the leader
and became a **candidate**. hashicorp/raft's **pre-vote** means a candidate first asks "would
you vote for me?" and only increments its term if a majority says yes. Nobody could hear
`kv-1`, so it kept asking at the same term, 2, for 38 seconds.

That's what made the heal invisible. Without pre-vote, `kv-1` would have bumped its term on
every attempt (one every second or two) and come back with term ≈ 20. Raft's rule is that a
higher term always wins, so the healthy leader would have seen term 20, stepped down, and
forced an election on a cluster that had nothing wrong with it. One flaky follower would
have caused a write outage for everyone.

Meanwhile `kv-1` kept answering **stale reads** from its frozen map, the whole time,
**397 probe values behind**: 40 seconds' worth. Nothing about a stale read says "this node
hasn't heard from the leader in 40 s". (Its `/status` does: `lastContact` and its role. A
smarter stale-read path could refuse to answer when `lastContact` is above a bound.)

## Takeaway

A minority partition costs **nothing** on the write path, and with pre-vote it costs nothing
when it heals either. The danger is entirely on the read side: an isolated node happily
serves 40-second-old data to anyone who asks it for a stale read.
