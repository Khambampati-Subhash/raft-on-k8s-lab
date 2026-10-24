# E8 — Slow and failing log writes

## What is injected (and what isn't)

The plan was Chaos Mesh `IOChaos` on `/data`. It can't run here: the arm64 chaos-daemon
image ships an x86-64 helper binary (see
[`chaos/unsupported/`](../../chaos/unsupported/README.md) and
[`results/E8-probe-20261024t092343.log`](results/E8-probe-20261024t092343.log)). Instead,
`kvnode --debug-endpoints` wraps its Raft **log store** in
[`node.FaultLogStore`](../../internal/node/faultstore.go), and `raftctl fault <id>` switches
faults on a live node:

- **delay:** every `StoreLog`/`StoreLogs` call sleeps first. That's once per call: a batch
  of entries pays it once.
- **fail-rate:** that fraction of calls return `EIO` without writing anything.

This is **log-append latency and errors**, not "a slow disk". Reads, the stable store (term
and vote) and snapshots are untouched. A real slow disk would hit all of them.

- **E8a** ([`chaos/E8a.sh`](../../chaos/E8a.sh)): +200 ms on every log append **on the
  leader**, for 40 s.
- **E8b** ([`chaos/E8b.sh`](../../chaos/E8b.sh)): 50 % of log appends **on one follower**
  fail with EIO, for 40 s.

## Hypothesis

_Written before the runs._

- **E8a:** in hashicorp/raft the leader writes new entries to its own log before handing
  them to replication, so every batch now costs ≥200 ms. Writers' requests batch up (4 at a
  time), so throughput during the fault collapses to roughly 4 per 200 ms ≈ 20/s, like E5a.
  Heartbeats come from separate replication goroutines and keep flowing, and lease contacts
  stay fresh: **no leader change**, no window over 500 ms (each batch takes ~200 ms),
  **Lost 0**.
- **E8b:** a follower whose append fails rejects that AppendEntries, and the leader retries
  it later. The leader still commits with the other, healthy follower, so clients see
  **nothing**: no window, no failed writes. The faulty follower falls behind and catches up
  in bursts, so its stale-read lag is higher than the healthy follower's. **No elections.
  Lost 0.**

## Observed

### E8a: +200 ms per log append on the leader

[`results/E8a-20261024t093017.log`](results/E8a-20261024t093017.log):

```
target: kv-0 (leader)
kv-0: delay=200ms fail-rate=0
kv-0: delay=0s fail-rate=0
## raftwatch events
                                            (none)
  writes: 208758 total, 208758 ok, 0 failed, 0 indeterminate (0 present, 0 absent)
  latency (ok): p50 1.505ms, p99 2.805ms
  unavailability windows: 7
    09:31:06.788 .. 09:31:07.399 (611ms)
    09:31:08.232 .. 09:31:08.847 (616ms)
    09:31:23.657 .. 09:31:24.273 (615ms)
    09:31:36.007 .. 09:31:36.618 (611ms)
    09:31:36.619 .. 09:31:37.243 (624ms)
    09:31:37.864 .. 09:31:38.480 (616ms)
    09:31:39.719 .. 09:31:40.337 (618ms)
  stale-read max lag: kv-0=0 kv-1=1 kv-2=0
  Lost: 0
```

### E8b: 50 % of one follower's appends fail

[`results/E8b-20261024t093324.log`](results/E8b-20261024t093324.log):

```
target: kv-1 (follower f1)
kv-1: delay=0s fail-rate=0.5
kv-1: delay=0s fail-rate=0
## raftwatch events
                                            (none)
  writes: 358847 total, 358847 ok, 0 failed, 0 indeterminate (0 present, 0 absent)
  unavailability windows: 0
  stale-read max lag: kv-0=0 kv-1=957 kv-2=0
  Lost: 0
```

Clients saw nothing. But a lag of **957** is impossible for a 40 s fault: at 10 probe writes
a second, 40 s can only open a gap of about 400. So we looked at the cluster after the run
([`results/E8b-stall-20261024t093850.log`](results/E8b-stall-20261024t093850.log)), **four
minutes** after the fault had been switched off:

```
kv-0 leader   term 2 commit 360052 lastLog 360052 lastContact 0
kv-1 follower term 2 commit 60889  lastLog 60889  lastContact 14.823041ms
kv-2 follower term 2 commit 360052 lastLog 360052 lastContact 38.4285ms
```

`kv-1` was **300,000 entries behind and not moving**, while hearing from the leader every
few milliseconds. Its last append failure was at 09:34:15. The leader's last line about it,
0.4 s later, was `pipelining replication`, and then nothing for four minutes. A second run
([`E8b-20261024t094005.log`](results/E8b-20261024t094005.log),
[`E8b-stall-20261024t094530.log`](results/E8b-stall-20261024t094530.log)) did exactly the
same: lag 993, `kv-1` frozen at index 52,647 against the leader's 368,184, last pipeline
line 0.18 s after the injection. **2 runs out of 2.**

| Prediction | Held? |
|---|---|
| E8a: throughput collapses, no leader change, Lost 0 | **Yes.** ~100k fewer writes than a fault-free run, no raftwatch events at all. |
| E8a: no window over 500 ms | **No.** 7 windows, all 611–624 ms. |
| E8b: clients see nothing, no elections, Lost 0 | **Yes.** 358,847 of 358,847 ok. |
| E8b: the faulty follower lags more, then catches up | **Lags, yes. Catches up: no.** It stopped replicating entirely and stayed stopped after the fault was gone. |

## Explanation

**E8a.** The leader writes each batch to its own log before replicating it, so every batch
waited 200 ms. Heartbeats come from separate goroutines and lease contacts stayed fresh:
nothing looked like a failure, and there was no election. The windows are tightly clustered
at **~3 × 200 ms**. That suggests a write sometimes queued behind two other delayed appends
(4 writers plus the stale-read probe share the leader's append path). We didn't trace the
exact sequence.

**E8b: a replication deadlock in hashicorp/raft v1.8.0.** We sent the leader `SIGQUIT` to get
a goroutine dump
([`E8b-stall-…-leader-goroutines.txt`](results/E8b-stall-20261024t093850-leader-goroutines.txt)).
The leader replicates to each follower over a **pipeline**: one goroutine sends AppendEntries
without waiting for replies, a second reads replies off the connection, and a third consumes
them. The dump shows:

```
goroutine 60 [select, 4 minutes]:                       ← sender, for kv-1
github.com/hashicorp/raft.(*netPipeline).AppendEntries(0x4001313bc0, …)
	net_transport.go:882        select { case n.inprogressCh <- future: … case <-n.shutdownCh: … }
github.com/hashicorp/raft.(*Raft).pipelineSend(…)
	replication.go:520

goroutine 123204 [select, 4 minutes]:                   ← reply reader, same pipeline
github.com/hashicorp/raft.(*netPipeline).decodeResponses(0x4001313bc0)
	net_transport.go:849        select { case n.doneCh <- future: … case <-n.shutdownCh: … }
```

and **no** `pipelineDecode` (the consumer) for that pipeline. Reading the v1.8.0 source:

1. The faulty follower rejected an AppendEntries (its append failed with EIO).
2. The consumer, `pipelineDecode`, treats a rejection as "abort pipeline" and **returns**. From
   then on nobody reads the pipeline's `doneCh`.
3. The reply reader fills `doneCh` (a bounded buffer) and then blocks on it forever, so it
   stops draining `inprogressCh`.
4. Under full write load the sender already had a full window of requests in flight, so
   `inprogressCh` fills up too, and the sender blocks forever at line 882.
5. The code that would close the pipeline (and unblock both, via `shutdownCh`) runs only
   after the sender returns. It never does.

The **heartbeat** goroutine for `kv-1` is separate and kept running, so `kv-1` reported a
healthy `lastContact` the whole time and never started an election. Nothing in the cluster
looked wrong except one number: `kv-1`'s commit index.

**What un-stuck it:** a leader change. Killing the leader (our `SIGQUIT`) made `kv-2`
leader with fresh pipelines, and `kv-1` caught up to 360,053 at once.

We haven't checked whether this is a known hashicorp/raft issue. A likely mitigation, not
tested here, is disabling pipelining (`MaxRPCsInFlight` < 2 in the transport config), at the
cost of throughput.

**Is the injector to blame?** It fails an append by returning EIO without writing anything.
That's the same thing bolt does when a write transaction fails: it rolls back. The deadlock
is in how the leader reacts to the follower's rejection, not in anything the follower did
wrong.

## Takeaway

A follower with a flaky disk didn't just fall behind: it **silently stopped replicating, for
good**, while looking healthy to every check we had (heartbeats fine, no elections, no client
errors, Lost 0). The cluster kept running on 2 of 3, one failure away from an outage, and the
stuck node served ever-staler stale reads. The only signal was a follower's commit index not
moving. That is the metric to alert on: **per-follower replication lag**, not liveness.
