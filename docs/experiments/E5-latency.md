# E5 — Slow network between the nodes

Two variants, same shape: every packet between kv pods is delayed for 40 s. Client traffic
(loadgen → pods) is not.

- **E5a**: 100 ms ± 50 ms each way.
- **E5b**: 800 ms ± 100 ms each way.

## Hypothesis

_Written before the runs._

- **A write costs one round trip to a follower**, so commit latency becomes about 2× the
  one-way delay: ~200 ms in E5a, ~1.6 s in E5b. With 4 sequential writers, throughput during
  the fault collapses from ~2,500/s to ~20/s (E5a) and ~2–3/s (E5b).
- **The whole-run p50/p99 will barely show it.** The fault produces very few writes, and the
  80 s around it produce ~200k fast ones, so 40 s of slow writes is well under 1% of the
  count. The visible signal is the **total write count** compared with E2 (312k, no fault).
- **E5a: no leader change.** 100 ms is far below the 1 s heartbeat timeout.
- **E5b: probably no leader change either.** Heartbeats go out every ~100 ms and arrive late
  but at the same rate, so followers keep hearing a leader. The risk is jitter or TCP
  hiccups opening a >1 s silence. E5b **will** show unavailability windows (>500 ms between
  ok writes, since each takes ~1.6 s), and maybe indeterminate writes if any exceed the 5 s
  apply timeout.
- **Lost: 0** for both.

## Injection

[`chaos/E5a-latency-100ms.yaml`](../../chaos/E5a-latency-100ms.yaml) and
[`chaos/E5b-latency-800ms.yaml`](../../chaos/E5b-latency-800ms.yaml): `NetworkChaos`
`action: delay` on every kv pod, targeting every kv pod, `direction: both`, 40 s.

## Observed

Logs: [`E5a`](results/E5a-20261024t090144.log), [`E5b`](results/E5b-20261024t090451.log).
For scale, fault-free 120 s runs of the same loadgen produce **312k–336k** writes (E2–E4).

| | E5a (100 ms) | E5b (800 ms) |
|---|---|---|
| total writes | 213,420 | 218,365 |
| ok / failed / indeterminate | 213,420 / 0 / 0 | 212,413 / 5,932 / 20 (16 present, 4 absent) |
| unavailability windows | 0 | **1, of 40.27 s** |
| leader changes | 0 | **5** (term 2 → 11) |
| p50 / p99 (whole run) | 1.50 / 2.80 ms | 1.48 / 2.66 ms |
| Lost | 0 | 0 |

E5b's raftwatch events, the start of 40 s of this:

```
09:05:39.295 (injected)
09:05:40.228 role          kv-0   leader -> follower
09:05:41.980 role          kv-0   follower -> candidate
09:05:43.730 term          kv-0   2 -> 3
09:05:45.227 leader-change kv-0   - -> kv-0
09:05:45.227 role          kv-0   candidate -> leader
09:05:45.728 role          kv-0   leader -> follower
...
09:05:52.978 leader-change kv-0   - -> kv-0
09:05:53.478 role          kv-0   leader -> follower
...
09:06:05.480 leader-change kv-0   - -> kv-0
09:06:05.979 role          kv-0   leader -> follower
...
09:06:19.978 leader-change kv-0   - -> kv-0       (term 10 -> 11, fault over, it stays)
```

| Prediction | Held? |
|---|---|
| Commit costs ~2× one-way delay; throughput collapses during the fault | **Yes.** Both runs are ~100k writes short of a fault-free run: the 40 s fault produced next to nothing. |
| Whole-run p50/p99 barely show it | **Yes, completely.** p99 is the same as in fault-free runs. The percentiles are blind to this; the totals aren't. |
| E5a: no leader change | **Yes.** No windows either: 4 writers at ~200 ms each still complete one every ~50 ms. |
| E5b: probably no leader change; windows from slow writes | **No, refuted.** 5 leaders in 40 s, and none of them lasted longer than 0.5 s. **Zero** writes succeeded for the whole fault: one 40.27 s window. |
| Lost 0 | **Yes**, both. |

## Explanation

**E5a** is what "slow but healthy" looks like. Every write waits for a follower's
acknowledgement, a ~200 ms round trip, so throughput dropped to a trickle. But heartbeats
arrive well inside the 1 s timeout, and leader-lease contacts well inside 500 ms, so nothing
else changed.

**E5b broke on the leader lease, not the heartbeat.** I reasoned about the follower side
(heartbeats arrive late, but at the same rate) and forgot the leader side. A hashicorp/raft
leader must hear back from a majority every `LeaderLeaseTimeout` (500 ms), or it steps down.
`kvnode`'s raft log says so in so many words:

```
raft: failed to contact quorum of nodes, stepping down
```

1. When the delay switched on, every reply in flight to the leader was pushed back ~800 ms.
   The leader went more than 500 ms without hearing from anyone and stepped down, 0.93 s
   after injection.
2. Elections still work: a vote request and its reply just take ~1.6 s. So a candidate
   eventually wins.
3. The new leader's first replies from followers can't arrive sooner than one round trip,
   about 1.6 s. Its lease runs out at 0.5 s. It steps down, **every time, after exactly
   0.5 s**: 45.227 → 45.728, 52.978 → 53.478, 05.480 → 05.979.

So once the round trip exceeds the lease timeout, **no node can stay leader**. The cluster is
up, all three nodes are healthy and talking to each other, and it serves zero writes until
the latency goes away.

**The 16 "present" indeterminates are the reason that class exists.** A short-lived leader
accepted a write, appended it, replicated it, and then lost leadership before it could
confirm the commit. The client got `504 outcome-unknown`. The *next* leader committed the
entry anyway, so the write is in the store. A client that treated that 504 as "failed" and
retried a non-idempotent operation would have applied it twice.

## Takeaway

Latency below the leader lease makes Raft **slow**. Latency above it makes Raft
**unavailable**, with no crash anywhere. hashicorp/raft's defaults (1 s heartbeat, 500 ms
lease) assume a LAN. A cluster stretched across regions, or one node on a congested link,
needs `--heartbeat-timeout`, `--election-timeout` and `--leader-lease-timeout` raised above its
worst round trip. Don't trust whole-run percentiles to tell you about any of this.
