# Stage 4 — Raft on a StatefulSet (the right way)

## What we tried

Stage 3 broke because a Deployment gives every replacement pod a new identity, a new address
and an empty disk. Raft needs the opposite. Kubernetes has an object built for exactly that,
the **StatefulSet**. The manifests are in [`deploy/base/`](../../deploy/base/):

```bash
make cluster-up deploy smoke     # deploy builds and loads the image first
make watch                       # live raftwatch table
```

Every decision below is in the manifests with a one-line comment. This section walks through
them; the next one shows two of them failing on purpose.

| Decision | What it fixes |
|---|---|
| **StatefulSet** `kv`, pods `kv-0`, `kv-1`, `kv-2` | The pod name is the Raft server ID, and a replacement pod gets the **same name**. No more ghost voters. |
| **Headless Service** `kv` | Gives each pod a stable DNS name, `kv-1.kv.raftlab.svc.cluster.local`. That name, not the pod IP, is the Raft address. The pod IP still changes on restart, but nobody depends on it. |
| `publishNotReadyAddresses: true` | A pod is only Ready once it knows a leader, and there's no leader until pods can resolve each other's names. Without this flag, DNS only lists Ready pods: deadlock. |
| **PVC per pod** (`volumeClaimTemplates`) | `kv-1`'s disk follows `kv-1`. A restarted member comes back with its log, its term and its vote, as Raft assumes. |
| `--bootstrap=auto` | Only `kv-0` bootstraps, and only if its disk has no Raft state. A restart with existing state never re-bootstraps. |
| `podManagementPolicy: Parallel` | Start all pods at once (shown below). |
| Liveness = `/healthz` (process alive), readiness = `/readyz` (knows a leader) | Never restart a pod for something a restart can't fix (shown below). |
| `podAntiAffinity` required on hostname | One member per node, so losing a node loses one vote, never two. |
| PDB `maxUnavailable: 1` | Drains and upgrades take down at most one member at a time, never a majority. |
| `fsGroup: 65532` | The image runs as non-root user 65532, and `fsGroup` makes the PVC writable for it. |

## What happened

### Smoke test: it holds

[`scripts/smoke.sh`](../../scripts/smoke.sh) checks each of these and fails fast:

```
ok:   Service kv publishes not-ready addresses
ok:   3/3 kv pods Ready
ok:   kv pods on 3 distinct nodes
ok:   one leader, voters exactly kv-0..kv-2, one term
....  loadgen 10s
loadgen: run smoke-1792773196, 4 workers for 10s
loadgen: verifying 23051 writes
run smoke-1792773196
  writes: 23051 total, 23051 ok, 0 failed, 0 indeterminate (0 present, 0 absent)
  latency (ok): p50 1.625ms, p99 3.563ms
  unavailability windows: 0
  Lost: 0
ok:   loadgen: Lost 0
ok:   kv-1 replaced and Ready
ok:   after restart: still one leader, voters exactly kv-0..kv-2 (no ghost)
ok:   new kv-1 pod serves smoke/marker from its PVC
SMOKE PASSED
```

The last three lines are stage 3's failure, fixed. Deleting `kv-1` produced a new pod
**named `kv-1`**. It rejoined as the same member (membership is still exactly
`kv-0..kv-2`), and it served a key written before the restart.

_Correction, found later in [E1](../experiments/E1-leader-kill.md#explanation):_ that last
check was weaker than it looked, and racy. A restarted member applies nothing until the
leader tells it how much of its log is committed, so right after Ready it can briefly serve
an empty store. A brand-new empty member would *also* eventually serve the key through
replication. `smoke.sh` now checks that `kv-1` got the **same PVC** back (same UID), which is
the real proof, and polls for the key instead of reading once.

(Write latency here is ~1.6 ms p50, against ~78 ms for the same loadgen on the macOS laptop
in stage 2. We didn't dig into why. The likely suspect is how expensive `fsync` is on macOS
compared with the Linux VM under kind, but that's a guess, not a measurement.)

### Trap 1: `OrderedReady`

A StatefulSet's default `podManagementPolicy` is `OrderedReady`: create `kv-0`, wait until
it's Ready, then `kv-1`, and so on. Our readiness means "knows a leader", and a leader needs
2 of 3 members. So the spec predicted: kv-0 waits for a majority, kv-1 waits for kv-0,
deadlock.

**Hypothesis we got wrong.** On a fresh cluster it works fine:

```
--- t=5s
kv-0 0/1 Running 0 raftlab-worker2
--- t=15s
kv-0 1/1 Running 0 raftlab-worker2
kv-1 1/1 Running 0 raftlab-worker
kv-2 0/1 ContainerCreating 0 raftlab-worker3
--- t=30s
kv-0 1/1 Running 0 raftlab-worker2
kv-1 1/1 Running 0 raftlab-worker
kv-2 1/1 Running 0 raftlab-worker3
```

On first start `kv-0` **bootstraps**: it creates a cluster whose only member is itself. One
of one is a majority, so it elects itself and is Ready alone. Then `kv-1` is created and
joins.

The deadlock is real, but it shows up **later**, on the first full restart: a node reboot,
a cluster upgrade, or just scaling down and back up. Now `kv-0` has existing state, a
3-member membership, so it doesn't bootstrap. It's 1 of 3:

```
$ kubectl -n raftlab scale statefulset/kv --replicas=0
$ kubectl -n raftlab scale statefulset/kv --replicas=3
--- t=10s
kv-0 0/1 Running 0
--- t=30s
kv-0 0/1 Running 0
--- t=90s
kv-0 0/1 Running 0
--- kv-0 log
raft: entering candidate state: node="Node at kv-0.kv.raftlab.svc.cluster.local:7000 [Candidate]" term=3
raft: entering candidate state: node="Node at kv-0.kv.raftlab.svc.cluster.local:7000 [Candidate]" term=3
--- events
Warning   Unhealthy   pod/kv-0   Readiness probe failed: HTTP probe failed with statuscode: 503
```

`kv-0` asks for votes forever. Kubernetes waits for it to be Ready before creating `kv-1`,
and that never happens. This is worse than failing on day one: it passes every test you run
on a fresh cluster, then takes the whole thing down on the first routine restart.

Same scale-down and scale-up with `Parallel` (the PVCs, and so the data, are kept):

```
--- t=10s
kv-0 1/1 Running 0
kv-1 1/1 Running 0
kv-2 1/1 Running 0
  kv-0 leader term 4 commit 6 servers ['kv-0', 'kv-1', 'kv-2']
  kv-1 follower term 4 commit 6 servers ['kv-0', 'kv-1', 'kv-2']
  kv-2 follower term 4 commit 6 servers ['kv-0', 'kv-1', 'kv-2']
```

All three start together, find each other, and elect a leader in seconds, with their data.
Raft brings its own ordering. It doesn't need Kubernetes to provide one.

### Trap 2: liveness that checks "is there a leader?"

Kubernetes has two probes, and they mean very different things:

- **readiness** failing: "don't send this pod traffic right now." Harmless.
- **liveness** failing: "this process is stuck, kill it and restart it."

It's tempting to point liveness at `/readyz` as well. We did that, with a twitchy
`failureThreshold: 1`:

```
$ kubectl -n raftlab patch statefulset kv --type=json -p '[{"op":"replace","path":"/spec/template/spec/containers/0/livenessProbe","value":{"httpGet":{"path":"/readyz","port":"http"},"periodSeconds":5,"failureThreshold":1}}]'
```

**During a leader election: a dice roll.** The spec predicted that killing the leader would
restart pods across the cluster. Here's how long a follower actually reports "no leader",
polling `/readyz` every 50 ms:

```
22:14:35.450 kill leader kv-0
22:14:36.646 kv-2 readyz=503
22:14:37.419 kv-1 readyz=503
22:14:37.426 kv-2 readyz=200
22:14:37.519 kv-1 readyz=200
```

A follower keeps reporting the *old* leader until its own heartbeat timeout fires. Only the
short vote itself is leaderless: 0.1–0.8 s here. A probe every 5 s lands in that window only
sometimes. In 6 deliberate leader kills we got **zero** liveness restarts:

```
round 1: killed leader kv-1 -> restarts now: kv-0=0 kv-1=0 kv-2=0
...
round 6: killed leader kv-0 -> restarts now: kv-0=0 kv-1=0 kv-2=0
```

But it did fire once, during the rolling update that applied the patch (which restarts the
leader too):

```
Warning   Unhealthy   pod/kv-1   Liveness probe failed: HTTP probe failed with statuscode: 503
Normal    Killing     pod/kv-1   Container kvnode failed liveness probe, will be restarted
```

So the prediction holds sometimes. A bug that shows up on roughly one election in a few is
the worst kind: it passes the test, then fires in production.

**During a lost majority: certain.** Take two members away. We cordoned their nodes and
deleted the pods; their disks are pinned to those nodes, so they stay `Pending`. `kv-0` is
left alone with no majority. It can't serve, and no restart of `kv-0` can change that:

```
$ kubectl cordon raftlab-worker raftlab-worker3; kubectl -n raftlab delete pod kv-1 kv-2
--- t=15s
  kv-0   false    Running   2        <none>
--- t=45s
  kv-0   false    Running   3        CrashLoopBackOff
--- t=90s
  kv-0   false    Running   4        CrashLoopBackOff
--- kv-0 events
  58s Warning Unhealthy pod/kv-0 Liveness probe failed: HTTP probe failed with statuscode: 503
  33s Normal Killing pod/kv-0 Container kvnode failed liveness probe, will be restarted
```

The one surviving member is killed over and over and ends up in **CrashLoopBackOff**. When
the other two come back, it may be sitting in a back-off delay of up to 5 minutes instead of
being there to vote. The outage gets longer, not shorter.

The same experiment with the real manifests (liveness = `/healthz`, "the process
answers HTTP"):

```
--- t=15s
  kv-0   false    Running   0        <none>
--- t=45s
  kv-0   false    Running   0        <none>
--- t=90s
  kv-0   false    Running   0        <none>
$ kubectl uncordon raftlab-worker raftlab-worker3
--- t=+30s after uncordon
  kv-0   true   Running   0     <none>
  kv-1   true   Running   0     <none>
  kv-2   true   Running   0     <none>
```

`kv-0` stays up and **not Ready**: no traffic, no restarts, log and term intact. As soon as
its peers return, there's a majority again and all three are Ready.

## Why it works

Each failure from stage 3 maps to one StatefulSet guarantee:

| Stage 3 failure | Stage 4 fix |
|---|---|
| Replacement pod gets a random name → ghost voter | Same pod name every time → same Raft ID |
| New pod IP → old address in the membership is useless | Raft address is a stable DNS name from the headless Service |
| `emptyDir` → restarted member forgets everything | PVC follows the pod → log, term and vote survive |
| "The seed always bootstraps" → empty twin cluster | Only `kv-0`, only with no existing state |

The two traps are the same mistake from opposite sides: letting Kubernetes act on "the
cluster has no leader", a condition that only Raft can fix. `OrderedReady` turns it into
"don't start the pods that would fix it". Liveness on `/readyz` turns it into "kill the
pods that are waiting to fix it". Readiness is the only probe that should know about
leaders.

## What we changed

- `deploy/base/` replaces stage 3's two Deployments with one StatefulSet, a headless Service
  with `publishNotReadyAddresses`, a client Service, a PDB and an in-cluster raftwatch.
- `scripts/smoke.sh` checks the whole thing in ~25 s (`make smoke`).
- `make deploy` builds and loads the image first. The image tag comes from `git describe`, so
  every commit changes it, and deploying an unloaded tag makes kubelet try Docker Hub.

## What we learned

- A consensus member needs **identity, address and disk** to survive a restart together.
  A StatefulSet plus a headless Service plus a PVC is exactly that.
- Test the **second start**, not just the first. `OrderedReady` passes on a fresh cluster and
  deadlocks on the first full restart.
- A probe that fails "sometimes" during elections is a bug that hides from tests. Measure
  the window instead of trusting one run.
- Liveness should answer only "is this process broken in a way a restart fixes?" A missing
  majority is never that.
