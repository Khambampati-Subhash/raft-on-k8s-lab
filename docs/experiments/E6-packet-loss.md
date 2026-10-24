# E6 — Lossy network between the nodes

Two variants: a fraction of packets between kv pods is dropped (each direction) for 40 s.

- **E6a**: 10 % loss.
- **E6b**: 40 % loss.

## Hypothesis

_Written before the runs._

- Raft's transport is TCP, so a lost packet means a **retransmit**, not a lost message. The
  first retransmit fires after ≥200 ms, then the wait doubles (400, 800 ms, …).
- **E6a (10 %)**: most round trips succeed first time. Some writes take 200–600 ms. **No leader
  change**: a >1 s heartbeat silence needs several consecutive losses on one connection,
  which is rare at 10 %. Fewer total writes than E2's 312k, and few or no windows.
- **E6b (40 %)**: a round trip only gets through cleanly about 36 % of the time
  (0.6 × 0.6), and retransmit backoff quickly reaches 800 ms–1.6 s. Followers will miss
  heartbeats for >1 s, so expect **repeated leader changes** (flapping) and **several
  unavailability windows**, with failed and indeterminate writes around each election.
- **Lost: 0** for both. Loss and elections can delay or refuse writes, never un-commit one.

## Injection

[`chaos/E6a-loss-10.yaml`](../../chaos/E6a-loss-10.yaml) and
[`chaos/E6b-loss-40.yaml`](../../chaos/E6b-loss-40.yaml): `NetworkChaos` `action: loss` on every
kv pod, targeting every kv pod, `direction: both`, 40 s.

## Observed

Logs: [`E6a`](results/E6a-20261024t090758.log), [`E6b`](results/E6b-20261024t091106.log).

| | E6a (10 %) | E6b (40 %) |
|---|---|---|
| total writes | 220,225 | 213,120 |
| ok / failed / indeterminate | 220,225 / 0 / 0 | 210,016 / 3,064 / 40 (36 present, 4 absent) |
| unavailability windows | 2 (546 ms, 589 ms) | **14**, longest 10.0 s |
| leader changes | 0 | **10** (term 2 → 12) |
| p99 (whole run) | 3.92 ms | 2.72 ms |
| stale-read max lag (followers) | 8, 12 | 97, 67 |
| Lost | 0 | 0 |

E6b's windows, all inside the 40 s fault:

```
    09:11:53.153 .. 09:11:53.765 (613ms)
    09:11:55.429 .. 09:11:56.244 (815ms)
    ...
    09:12:00.302 .. 09:12:02.982 (2.68s)
    09:12:02.984 .. 09:12:12.999 (10.015s)
    09:12:13.273 .. 09:12:21.765 (8.494s)
    ...
    09:12:26.231 .. 09:12:33.150 (6.919s)
```

and the leader's own log, repeatedly:

```
raft: failed to contact quorum of nodes, stepping down
```

| Prediction | Held? |
|---|---|
| E6a: some slow writes, no leader change, few windows | **Yes.** No elections, two ~0.55 s stalls, p99 up from ~2.7 to 3.9 ms. |
| E6b: repeated leader changes, several windows, failed + indeterminate around elections | **Yes.** 10 leader changes, 14 windows, 3,064 failed, 40 indeterminate. |
| Lost 0 | **Yes**, both. |

## Explanation

Every Raft message rides on TCP, so a dropped packet is retransmitted, not lost. The cost is
time: Linux waits at least 200 ms before the first retransmit and doubles the wait each time.

At **10 %**, almost every exchange gets through on the first or second try. The two ~0.55 s
windows are moments when a replication round needed a retransmit or two. That's slow, but
inside every Raft timeout.

At **40 %**, a request *and* its reply both survive only 36 % of the time, and backoff
quickly reaches whole seconds. Two different timers trip:

- **The leader's lease (500 ms):** it can't hear from a majority in time and steps down (the
  log line above).
- **The followers' heartbeat timeout (1 s):** they start elections, which themselves need
  messages to get through.

The result is **flapping**: 10 leaders in 40 s, each serving for a few seconds at most, with
gaps of up to 10 s in between. Writes during each gap fail (no leader). Writes caught at a
step-down come back indeterminate. As in E5b, most of those (36 of 40) turned out to be
**present**: committed by the next leader after the client had been told "unknown".

The followers' stale-read lag (97 and 67) shows how far behind they fell while replication
kept getting interrupted.

## Takeaway

Raft degrades with packet loss rather than falling off a cliff, but the slope is steep: 10 %
is a non-event, 40 % is a leader carousel with 10-second outages. Same as everywhere else in
this lab: **availability suffers, correctness doesn't**. 210,016 acknowledged writes,
0 lost.
