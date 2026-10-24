# E10 — Rolling restart under load

What every image upgrade or config change does: `kubectl rollout restart sts/kv`
([`chaos/E10.sh`](../../chaos/E10.sh)). The StatefulSet replaces pods **one at a time**,
highest ordinal first (`kv-2`, `kv-1`, `kv-0`), waiting for each to be Ready before the next.

## Hypothesis

_Written before the run._

- On a fresh cluster the leader is `kv-0` (it bootstrapped), and it's restarted **last**.
  Restarting each follower costs nothing: no window, no election (like E2).
- Restarting the leader costs **one election**, like E1: a 1–3 s window, a few failed writes.
  `kvnode` doesn't hand leadership over on SIGTERM, so the followers have to time out first.
- Readiness gates the rollout ("knows a leader"), and the PDB allows one member down at a
  time, so the cluster never loses its majority. **Exactly 1 leader change. Lost 0.**

## Observed

[`results/E10-20261024t102708.log`](results/E10-20261024t102708.log), the full raftwatch
event stream:

```
10:27:58.385 (rollout restart)
10:27:59.562 unreachable   kv-2
10:28:00.812 reachable     kv-2   role=follower term=2
10:28:01.312 unreachable   kv-1
10:28:02.812 reachable     kv-1   role=follower term=2
10:28:04.312 role          kv-1   follower -> candidate
10:28:11.562 role          kv-1   candidate -> follower
10:28:13.312 unreachable   kv-0
10:28:14.813 role          kv-1   follower -> candidate
10:28:15.064 reachable     kv-0   role=follower term=2
10:28:15.563 leader-change kv-2   - -> kv-2
10:28:15.563 term          kv-0/kv-1/kv-2   2 -> 3
```

```
  writes: 309090 total, 309070 ok, 20 failed, 0 indeterminate (0 present, 0 absent)
  unavailability windows: 1
    10:28:13.306 .. 10:28:18.438 (5.131s)
  stale-read max lag: kv-0=0 kv-1=330 kv-2=13
  Lost: 0
```

| Prediction | Held? |
|---|---|
| Follower restarts cost nothing | **For clients, yes:** no window while `kv-2` and `kv-1` restarted. But see `kv-1` below. |
| Leader restart costs one election | **Yes:** exactly one `leader-change` (`kv-2`, term 3), when `kv-0` went last. |
| …and a 1–3 s window | **No: 5.1 s.** |
| Lost 0 | **Yes.** |

## Explanation

**The rollout itself behaved.** One pod at a time, the leader last (it's `kv-0`, and
StatefulSets update highest ordinal first), never more than one member down. The single
election was the leader's own restart.

**The window: 2.25 s of election plus 2.9 s we can't account for.** From the leader going
unreachable (13.31) to the new leader (15.56) is the usual timeout-and-vote. Writes then
took another 2.9 s to resume (18.44). We suspected client discovery stalling on the dead pod's
IP (`Statuses()` waits for every node, with a 5 s client timeout), and tested it: we deleted a
leader pod the same way and timed `raftctl status`, which uses the same discovery. Every call
returned in **0.1 s**. That hypothesis is refuted. The remaining 2.9 s is **unexplained**.
E9b showed the same 5.1 s shape after a graceful leader deletion, while E1's pod-kill was
1.25 s, so it's tied to how the old leader goes away. It's worth chasing before relying on
these numbers.

**`kv-1` spent 7 s as a candidate, and nobody noticed.** After its restart, `kv-1` was back
as a follower at 02.8, became a candidate 1.5 s later, and stayed one until 11.56. The leader
`kv-0` was healthy the whole time and `kv-2` was replicating fine, so clients saw nothing
(pre-vote kept `kv-1` from disturbing anyone, as in E3). But `kv-1` wasn't receiving
heartbeats or entries, so its stale-read lag reached 330. The timing points at the transport:
`kv-1`'s old pod went away at 01.31, and it recovered at 11.56, about **10 s** later, which is
the timeout our Raft transport uses (`NewNetworkTransport(…, 10*time.Second, …)`). Our DNS
transport resolves `kv-1`'s name on every *new* connection, but the leader can keep using a
pooled connection to the *old* pod IP until a request on it times out. That is our reading of
the timing, not a traced fact.

## Takeaway

A rolling restart under load cost **one election and Lost 0**, as it should. Two easy wins
showed up:

- `kvnode` could hand leadership to a follower on SIGTERM (`raft.LeadershipTransfer`) instead
  of making the cluster time out. That turns the leader's restart into a planned hand-off.
- A shorter transport timeout (or dropping pooled connections when a peer's DNS name moves to
  a new IP) would stop a restarted follower from going deaf for 10 s.

Neither is implemented here.
