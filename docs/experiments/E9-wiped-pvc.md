# E9 — A node loses its disk

A pod's PVC is deleted and the pod restarted: the node comes back with the **same name and
address but an empty disk**. On kind, local-path volumes are deleted with the claim, so the
data is really gone. [`scripts/wipe-pvc.sh`](../../scripts/wipe-pvc.sh) does it under load.

- **E9a**: wipe follower `kv-2`.
- **E9b**: wipe `kv-0`, the one pod `--bootstrap=auto` allows to bootstrap a new cluster.

## Hypothesis

_Written before the runs._

- **E9a:** `kv-2` comes back empty, isn't `-0` so it doesn't bootstrap, and asks to join. It's
  already in the membership under the same ID and address, so the join is a no-op. The leader
  sees an empty follower and replays the log (or sends a snapshot). No election, no window,
  **Lost 0**.
- **E9b, what the plan predicted:** a **split brain**. `kv-0` comes back empty, bootstraps a
  new one-node cluster, and elects itself, while `kv-1`/`kv-2` (still 2 of 3) carry on with
  their own leader. Two leaders. Loss or divergence expected.
- **E9b, what I expect instead:** the empty `kv-0` **is absorbed** back into the old cluster
  within a second or two. Its new cluster starts at term 1–2, and the old cluster's leader
  is at a higher term or the same one. That leader's next heartbeat reaches `kv-0` at the
  same DNS address, and Raft's rule makes `kv-0` step down and follow it. The leader then
  overwrites `kv-0`'s one-entry log with its own. **The danger is the moment before that:**
  if any client write lands on `kv-0` while it's a one-node leader, it is acknowledged by a
  "cluster" of one and then truncated: **an acknowledged write lost**. loadgen picks the
  higher-term leader, so I expect that window to be missed: **Lost 0, but by luck, not by
  design**.

### E9c (added after E9b; hypothesis written before its run)

E9b's outcome depended on a race (see below). **E9c** removes the race. `kv-1` and `kv-2` go
dark (100 % packet loss on all their traffic, [`chaos/E9c.sh`](../../chaos/E9c.sh)) for
50 s, and `kv-0` is wiped 3 s in. Prediction: `kv-0` can reach nobody, so `--bootstrap=auto`
lets it **bootstrap a one-node cluster and lead it**. It's the only node clients can reach,
so loadgen writes to it, and it **acknowledges them** (a majority of one). When the network
heals, `kv-1`/`kv-2` (still 2 of the *old* 3) elect a leader at a higher term, that leader
overwrites `kv-0`'s log, and every write `kv-0` acknowledged disappears. **Lost > 0.** This is
the split brain the plan was looking for.

## Injection

[`scripts/wipe-pvc.sh`](../../scripts/wipe-pvc.sh) `<N>`: delete PVC `data-kv-N` (kept alive
by `pvc-protection` while the pod runs), delete pod `kv-N`, wait until a **new** `data-kv-N`
(new UID) is `Bound` and `kv-N` is Ready, then print `raftctl status`. E9c also runs
[`chaos/E9c.sh`](../../chaos/E9c.sh) first.

**Which build ran which run.** The bootstrap fix below was deployed at ~10:15 UTC. Runs
before that used the old `--bootstrap=auto` (any empty `-0` bootstraps): E9a
[`095641`](results/E9a-20261024t095641.log), E9b [`100053`](results/E9b-20261024t100053.log),
E9c [`100620`](results/E9c-20261024t100620.log). Runs after it: E9b
[`101613`](results/E9b-20261024t101613.log), E9c [`101933`](results/E9c-20261024t101933.log).
(The new kvnode logs `bootstrap=auto has-state=…` at startup, and `exp.sh` now writes the
image tag into every results log.)

## Observed, before the fix

### E9a: wipe follower `kv-2`

```
target: kv-2 (old PVC uid 7d447457-…)
new PVC: b49fa3e1-… Bound
kv-2 Ready again at 09:58:11.882
  kv-0 role=leader   term=2 commit=84404 servers=['kv-0', 'kv-1', 'kv-2']
  kv-1 role=follower term=2 commit=84401
  kv-2 role=follower term=2 commit=84401
## raftwatch events
09:58:08.082 unreachable   kv-2
09:58:11.581 reachable     kv-2   role=follower term=2
  writes: 311210 total, 311210 ok, 0 failed, 0 indeterminate
  unavailability windows: 0
  Lost: 0
```

Held: an empty `kv-2` rejoined under its old ID and was fully replicated within 5 s of being
Ready. No election, no window.

### E9b: wipe `kv-0`

```
kv-0 Ready again at 10:01:46.249
  kv-0 role=follower term=3 leader=kv-1 commit=69866
  kv-1 role=leader   term=3 leader=kv-1 commit=69867
  kv-2 role=follower term=3 leader=kv-1 commit=69866
  writes: 299480 total, 299464 ok, 16 failed, 0 indeterminate
  unavailability windows: 1   (5.099s)
  Lost: 0
```

No split brain. But `kv-0`'s own log shows it **did** bootstrap a new cluster:

```
10:01:45.711 kvnode kv-0: … bootstrap=true
10:01:46.930 raft: failed to get previous log: previous-index=57259 last-index=1 error="log not found"
```

`last-index=1` is its own one-member bootstrap entry. The old cluster's leader (`kv-1`,
term 3) reached it **1.2 s** after it started, before its election timer fired, so it never
got to lead its one-node cluster. It stepped down to the higher term and was overwritten.
My "absorbed" prediction held, but **only because a race went the right way**.

### E9c: wipe `kv-0` while `kv-1` and `kv-2` are dark

Prediction (above): split brain, Lost > 0. [`results/E9c-20261024t100620.log`](results/E9c-20261024t100620.log):

```
kv-1, kv-2: 100% packet loss for 50s
target: kv-0 (old PVC uid 9ede3450-…)
kv-0 Ready again at 10:07:29.140
  kv-0 role=leader term=2 leader=kv-0 commit=14097 servers=['kv-0']
  http://10.244.1.48:8080 unreachable
  http://10.244.3.66:8080 unreachable
## raftwatch events
10:07:27.623 reachable     kv-0   role=follower term=1
10:07:29.127 leader-change kv-0   - -> kv-0           ← a cluster of one
10:08:00.187 reachable     kv-1   role=follower term=2
10:08:03.561 leader-change kv-2   kv-0 -> kv-2
10:08:03.561 term          kv-0   2 -> 3
10:08:03.561 role          kv-0   leader -> follower
  writes: 339465 total, 339452 ok, 3 failed, 10 indeterminate (2 present, 8 absent)
  unavailability windows: 1   (20.6s)
  Lost: 170972
```

**Held, all of it.** The empty `kv-0` bootstrapped `servers=['kv-0']`, elected itself, and
for 34 s was the only node any client could reach, so it acknowledged **170,972 writes**
with a "majority" of one. When the network healed, `kv-2` won term 3 with `kv-1`'s vote,
`kv-0` stepped down, and its log was overwritten. Every one of those writes is gone.

(`kv-1` and `kv-2` were each restarted twice during the run: 100 % packet loss also drops
the replies to kubelet's liveness probes. That's a side effect of how the fault was injected,
not of Raft. `exp.sh` flagged it.)

## Why

Raft has two assumptions that a wiped disk breaks:

1. **Bootstrap happens exactly once per cluster.** `--bootstrap=auto` meant "any empty
   `kv-0` bootstraps". After a wipe, `kv-0` is empty again and has no way to know a cluster
   exists, unless it asks.
2. **A voter never forgets.** A vote, a term and a log are promises. A node that comes back
   with none of them can break those promises without knowing it.

## What we changed

[`cmd/kvnode/bootstrap.go`](../../cmd/kvnode/bootstrap.go): with `--bootstrap=auto`, an empty
`kv-0` no longer bootstraps on its own say-so. It starts with Raft idle, serves `/healthz`
(so liveness doesn't kill it while it waits), and asks the other members for their
`/status` every second:

| Peers say | Decision |
|---|---|
| some peer has `term > 0` | a cluster exists: **join it** |
| every peer answered and all have `term 0` | a fresh cluster: **bootstrap** |
| some peer didn't answer, none has a term | can't tell: **keep waiting** |

The plan called for bootstrapping when *every* peer is unreachable, since that's what a
cold start looks like. E9c is why this version refuses: "unreachable" is exactly what a live
cluster looks like from the wrong side of a partition. The cost: a brand-new cluster forms
only once every member is up (the StatefulSet starts all three at once, and this lab's cold
start took about 1 s). `--bootstrap=true` overrides it.

## Observed, after the fix

E9b ([`101613`](results/E9b-20261024t101613.log)): `kv-0` comes back at **term 0**, so it
didn't bootstrap, and joins.

```
10:17:06.325 reachable     kv-0   role=follower term=0
10:17:06.574 term          kv-0   0 -> 3
  Lost: 0
```

E9c again ([`101933`](results/E9c-20261024t101933.log)). _There was no separate written
hypothesis for this rerun; the expectation was simply "the fix prevents the split brain"._

```
10:20:49.165 reachable     kv-0   role=follower term=0       ← waited, did not bootstrap
10:21:15.058 leader-change kv-2   - -> kv-2
10:21:15.058 term          kv-0   0 -> 3
  writes: 179602 total, 179552 ok, 33 failed, 17 indeterminate (0 present, 17 absent)
  unavailability windows: 1   (51.072s)
  Lost: 75
```

The split brain is gone: `kv-0` waited out the whole outage at term 0, and writes were
**unavailable** for 51 s instead of being accepted by an impostor. But **75 acknowledged
writes were still lost**, and the raft logs show how:

```
kv-1: rejecting vote request since our last index is greater: candidate=kv-2… last-index=51855 last-candidate-index=51778
kv-2: election won: term=3 tally=2
kv-1: clearing log suffix: from=51779 to=51855
```

1. As the fault went in, the old leader `kv-0` and `kv-1` committed 77 more entries together
   (Chaos Mesh applied the loss to `kv-2` a moment before `kv-1`). That was a majority, so
   clients were told "ok".
2. Then `kv-0` was wiped: one of the two copies of those entries was gone.
3. At heal, `kv-2` (77 entries behind) ran for leader. `kv-1` correctly refused. But the
   **empty** `kv-0` voted yes: an empty log counts as "no more up to date than anyone". Two
   votes out of three: `kv-2` won without the committed entries.
4. The new leader made `kv-1`'s log match its own: `clearing log suffix` deleted the 77
   entries. 75 of them were acknowledged loadgen writes.

So the bootstrap fix is **necessary, not sufficient**. Any voter that loses its disk, not
just `kv-0`, can help elect a leader that's missing committed writes. Whether it happens
depends on a race (which follower lagged, and whose timer fires first after the heal), so
the honest statement is **can lose acknowledged writes**, not "always does". E9a didn't
because no election happened before `kv-2` caught up.

**The real fix** is the standard one: a member that comes back with no state must rejoin as
a **non-voter**. The leader removes it and re-adds it with `AddNonvoter`, the node doesn't
start Raft (so it can't vote) until that has committed, and it's promoted back to voter only
once it has caught up. That's what etcd's "remove and re-add the member" procedure and
Consul's staged non-voters exist for. It isn't implemented in this lab yet.

## Takeaway

Raft's safety rests on durable storage: **a voter must never forget**. Losing a disk isn't
"a node restarting", it's "a node that lies about what it promised". Two things follow:

- Never let a node with no state decide on its own that no cluster exists (the bootstrap
  fix: 170,972 lost writes → none from that cause).
- Never let a node with no state vote until it has caught up (not fixed here: 75 lost writes
  in the run that tested it).
