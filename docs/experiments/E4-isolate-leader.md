# E4 — Partition the leader away from both followers

## Hypothesis

_Written before the run._

- The two followers stop hearing heartbeats, and one of them wins a **new term** within ~1–3 s.
- The old leader can reach nobody, so after its **leader lease** (500 ms) without hearing from
  a majority it **steps down**. While isolated, its term stays where it was (pre-vote). On
  heal it sees the higher term and becomes a follower of the new leader.
- Writes on the old leader at the moment of the cut can't commit. At step-down they fail with
  "leadership lost" → `504 outcome-unknown` → **indeterminate (at most one per writer, ≤4)**,
  almost certainly **absent** on read-back. After step-down the old leader answers 503/421,
  and clients find the new leader.
- **No write sent to the isolated old leader comes back ok.** One unavailability window of
  ~1–3 s. **Lost: 0.**

## Injection

[`chaos/E4-isolate-leader.yaml`](../../chaos/E4-isolate-leader.yaml): a `NetworkChaos`
`partition`, `direction: both`, between `raft-role=leader` and `raft-role=follower`.
Clients can still reach all three pods, including the cut-off leader.

```
leader before injection: kv-0
  kv-0 Running leader l
  kv-1 Running follower f1
  kv-2 Running follower f2
injected at 2026-10-24T08:57:39.246+00:00
chaos record: e4-isolate-leader raftlab/kv-0 phase=Not Injected injected=1
chaos record: e4-isolate-leader raftlab/kv-1 phase=Not Injected injected=1
chaos record: e4-isolate-leader raftlab/kv-2 phase=Not Injected injected=1
```

## Observed

From [`results/E4-20261024t085649.log`](results/E4-20261024t085649.log):

```
08:57:39.924 role          kv-0   leader -> follower
08:57:40.671 role          kv-1   follower -> candidate
08:57:41.673 leader-change kv-2   - -> kv-2
08:57:41.673 role          kv-0   follower -> candidate
08:57:41.673 term          kv-1   2 -> 3
08:57:41.673 role          kv-1   candidate -> follower
08:57:41.673 term          kv-2   2 -> 3
08:57:41.673 role          kv-2   follower -> leader
08:58:19.422 term          kv-0   2 -> 3
08:58:19.422 role          kv-0   candidate -> follower
```

```
  writes: 336087 total, 335819 ok, 264 failed, 4 indeterminate (0 present, 4 absent)
  latency (ok): p50 1.358ms, p99 2.616ms
  unavailability windows: 1
    08:57:39.397 .. 08:57:41.554 (2.156s)
  stale-read max lag: kv-0=415 kv-1=0 kv-2=0
  Lost: 0
```

| Prediction | Held? |
|---|---|
| A follower wins a new term in ~1–3 s | **Yes.** `kv-2`, term 3, 2.4 s after the cut. |
| Old leader steps down after its lease, keeps its term while isolated, follows on heal | **Yes.** `leader -> follower` 0.68 s after the cut. Term stays 2 for 40 s, then `2 -> 3` and `follower` at the heal. |
| In-flight writes → indeterminate, ≤4, absent | **Yes: exactly 4** (one per writer), **all absent**. |
| No write via the old leader comes back ok; one 1–3 s window; Lost 0 | **Yes.** One window, 2.16 s. Lost 0. |

## Explanation

This is the scenario Raft was designed for: two nodes that both have a claim to be leader.

**The old leader gives up on its own.** A hashicorp/raft leader keeps a **leader lease**: if it
hasn't heard from a majority within `LeaderLeaseTimeout` (500 ms by default), it steps down.
raftwatch saw `kv-0` go `leader -> follower` 0.68 s after the cut, which is the 500 ms lease
plus up to 250 ms of raftwatch polling. From then on, it stopped accepting writes.

**Its last writes are honestly "unknown".** Each of the 4 writers had one request sitting in
`kv-0`'s log, waiting for a majority that would never answer. At step-down, raft failed them
with "leadership lost". The entry *might* have reached a majority before the cut and committed
later, so `kvnode` answers `504 outcome-unknown`, and loadgen counts **4 indeterminate**. On
read-back all 4 were **absent**: they never left `kv-0`. Had any of them been answered "ok",
it would have been an acknowledged write that doesn't exist, and loadgen's verify would have
counted it as Lost. Lost is 0.

**Then the client finds the new leader.** After a non-2xx answer the client forgets the
leader it remembered and asks every node for `/status`. If two nodes claim to be leader (for
a moment, `kv-0` still believed it was), it picks the **higher term**. The 264 `failed` writes
are the ones sent while there was no leader at all: refused, definitely not applied.

**Why the old leader's term didn't run away.** Like E3's follower, `kv-0` sat as a candidate
for 38 s at term 2. Pre-vote kept it from inflating its term and disrupting `kv-2` on heal. It
came back, saw term 3, and followed.

**The isolated side kept serving stale reads.** `kv-0`'s stale-read lag hit **415**: for 40 s
the former leader answered stale reads from data frozen at the moment of the cut.

## Takeaway

Even when clients can still reach a cut-off leader, it **cannot acknowledge a single write**.
It needs a majority to commit, and it gives up leadership after 500 ms without one. What
clients see is a ~2 s window with a few honestly-unknown writes, none of which turned out to
exist. Stale reads remain the soft spot: the ex-leader served 40 s of frozen data.
