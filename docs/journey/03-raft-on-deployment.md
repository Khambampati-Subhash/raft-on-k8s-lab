# Stage 3 — Raft on a Kubernetes Deployment (the wrong way)

## What we tried

Stage 2 worked on one laptop. The obvious next step is to wrap the same three processes in
the most common Kubernetes object, a **Deployment**, and change as little as possible:

| Stage 2 (laptop) | Stage 3 (Deployment) |
|---|---|
| `--id n1` | `--id=$(POD_NAME)` (e.g. `kv-rest-55cfc4ff5-26pxw`) |
| `--raft-advertise 127.0.0.1:7001` | `--raft-advertise=$(POD_IP):7000` |
| `--data $TMPDIR/raftlab/n1` | `--data=/data` on an `emptyDir` |
| n1 bootstraps, n2/n3 join n1 | Deployment `kv-seed` (1 pod, `--bootstrap=true`); Deployment `kv-rest` (2 pods, `--join=http://kv-seed:8080`) |

The manifests are in [`deploy/stage3-deployment/`](../../deploy/stage3-deployment/). The demo:

```bash
make cluster-up image load-image
make stage3-demo        # scripts/stage3-demo.sh
```

The script deploys, writes two keys, then does the most ordinary thing that happens to
pods: it deletes them, one at a time, and lets Kubernetes replace them.

## What happened

### It starts fine

```
== deploy (seed bootstraps, two rest pods join it)
  NAME IP NODE
  kv-rest-55cfc4ff5-26pxw 10.244.3.8 raftlab-worker2
  kv-rest-55cfc4ff5-6xzxn 10.244.2.10 raftlab-worker3
  kv-seed-7c4d67d96c-ndj5h 10.244.1.8 raftlab-worker
  kv-rest-55cfc4ff5-26pxw  role=follower  term=2   leader=kv-seed-7c4d67d96c-ndj5h servers=3
  kv-rest-55cfc4ff5-6xzxn  role=follower  term=2   leader=kv-seed-7c4d67d96c-ndj5h servers=3
  kv-seed-7c4d67d96c-ndj5h role=leader    term=2   leader=kv-seed-7c4d67d96c-ndj5h servers=3
  membership according to kv-seed-7c4d67d96c-ndj5h:
    kv-seed-7c4d67d96c-ndj5h 10.244.1.8:7000    alive
    kv-rest-55cfc4ff5-6xzxn  10.244.2.10:7000   alive
    kv-rest-55cfc4ff5-26pxw  10.244.3.8:7000    alive

== write city=paris and lang=go through the leader (kv-seed-7c4d67d96c-ndj5h)
  PUT city -> 204
  PUT lang -> 204
```

Same picture as stage 2: one leader, three **voters**, writes committed. Nothing here hints
at a problem.

### (a) Replace one pod: the cluster grows a ghost

```
== (a) delete one kv-rest pod (kv-rest-55cfc4ff5-26pxw); the Deployment replaces it
  kv-rest-55cfc4ff5-6xzxn  role=follower  term=2   leader=kv-seed-7c4d67d96c-ndj5h servers=4
  kv-rest-55cfc4ff5-tf665  role=follower  term=2   leader=kv-seed-7c4d67d96c-ndj5h servers=4
  kv-seed-7c4d67d96c-ndj5h role=leader    term=2   leader=kv-seed-7c4d67d96c-ndj5h servers=4
  membership according to kv-seed-7c4d67d96c-ndj5h:
    kv-seed-7c4d67d96c-ndj5h 10.244.1.8:7000    alive
    kv-rest-55cfc4ff5-6xzxn  10.244.2.10:7000   alive
    kv-rest-55cfc4ff5-26pxw  10.244.3.8:7000    DEAD
    kv-rest-55cfc4ff5-tf665  10.244.3.9:7000    alive
  PUT color=blue -> 204
```

The replacement pod got a **new name** (`tf665`), a **new IP** (`10.244.3.9`) and an
**empty disk**. To Raft it is a stranger, so it joined as a fourth member. The old member
`26pxw` is still in the membership list. Raft never removes a member just because it
stopped answering. Only an explicit "remove server" entry in the log does that, and nobody
sent one.

So we now have 4 voters, 1 of which will never come back: a **ghost voter**. The majority
of 4 is 3, and we have exactly 3 alive. Writes still work, but the cluster can no longer
survive *any* failure, even though three pods are running and Kubernetes says all is well.

### (b) Replace the second pod: it depends on a race

```
== (b) delete the other original kv-rest pod (kv-rest-55cfc4ff5-6xzxn)
  kv-rest-55cfc4ff5-tf665  role=follower  term=2   leader=kv-seed-7c4d67d96c-ndj5h servers=5
  kv-rest-55cfc4ff5-zdm9h  role=follower  term=2   leader=kv-seed-7c4d67d96c-ndj5h servers=5
  kv-seed-7c4d67d96c-ndj5h role=leader    term=2   leader=kv-seed-7c4d67d96c-ndj5h servers=5
  membership according to kv-seed-7c4d67d96c-ndj5h:
    kv-seed-7c4d67d96c-ndj5h 10.244.1.8:7000    alive
    kv-rest-55cfc4ff5-6xzxn  10.244.2.10:7000   DEAD
    kv-rest-55cfc4ff5-26pxw  10.244.3.8:7000    DEAD
    kv-rest-55cfc4ff5-tf665  10.244.3.9:7000    alive
    kv-rest-55cfc4ff5-zdm9h  10.244.1.9:7000    alive
  last join attempts from kv-rest-55cfc4ff5-zdm9h:
    2026/10/23 16:26:40.777145 join: kv-rest-55cfc4ff5-zdm9h joined as 10.244.1.9:7000
  PUT color=red -> 204
```

5 voters, 2 ghosts, 3 alive: just enough. But look at the timing in raftwatch's events:

```
16:26:40.53399234Z unreachable   kv-rest-55cfc4ff5-6xzxn
16:26:40.782519132Z reachable    kv-rest-55cfc4ff5-zdm9h role=follower term=2
```

For the moment between those two lines, the cluster had 4 voters and only 2 alive (seed +
`tf665`): **no majority**. It survived only because the replacement pod started and joined
within about a quarter of a second, before the leader noticed it had lost its majority and
stepped down. (Raft uses a new membership list as soon as it's appended, so once
`zdm9h`'s "add voter" entry was in the log, 3 of 5 was enough to commit it.) On kind, with
the image already on the node, pods start very fast. On a real cluster, with an image pull,
they don't.

**Hypothesis we got wrong.** The plan for this stage said: "delete both kv-rest pods → new
pods join, status shows 5 servers, 2 dead." The first version of the script did exactly
that, deleting both at once. That's not what happened:

```
http://10.244.1.3:8080 kv-rest-55cfc4ff5-5qsp7 follower 0  [] None
http://10.244.3.3:8080 kv-rest-55cfc4ff5-fb4bl candidate 2  ['dnw9p', 'xmkk7', 's8p8l', 'fb4bl'] None
http://10.244.1.2:8080 kv-seed-7c4d67d96c-dnw9p candidate 2  ['dnw9p', 'xmkk7', 's8p8l', 'fb4bl'] None
-- pod/kv-rest-55cfc4ff5-5qsp7
join: /join: HTTP 503: no leader (retrying in 5s)
join: /join: HTTP 503: no leader (retrying in 5s)
-- pod/kv-rest-55cfc4ff5-fb4bl
join: /join: HTTP 504: outcome-unknown (retrying in 200ms)
join: kv-rest-55cfc4ff5-fb4bl is already a member
```

With both followers gone, the seed was 1 of 3. One replacement (`fb4bl`) still got its
"add voter" entry appended. Its join came back `504 outcome-unknown`, which is honest: the
leader couldn't confirm the commit. That left **4 voters, 2 alive**. That's no majority,
and there's no way to get one: the second replacement (`5qsp7`) can't join without a
leader, and there can't be a leader without it. Both live members sit as candidates
forever. The cluster is **permanently wedged**. Nothing short of manual surgery (rewriting
the membership on disk) brings it back.

So "5 servers, 2 dead" is what you get when you win the race. A wedged cluster is what you
get when you lose it.

(The candidates' term stays at 2 while they try to get elected. hashicorp/raft uses
**pre-vote**: a candidate first asks "would you vote for me?" and only bumps the term if it
could actually win.)

### (c) Replace the seed: a brand-new, empty cluster

```
== (c) delete the seed pod (kv-seed-7c4d67d96c-ndj5h); its replacement starts empty and bootstraps again
  NAME READY IP NODE
  kv-rest-55cfc4ff5-tf665 0/1 10.244.3.9 raftlab-worker2
  kv-rest-55cfc4ff5-zdm9h 0/1 10.244.1.9 raftlab-worker
  kv-seed-7c4d67d96c-ndj5h 0/1 10.244.1.8 raftlab-worker
  kv-seed-7c4d67d96c-vp9d4 1/1 10.244.2.12 raftlab-worker3
  kv-rest-55cfc4ff5-tf665  role=candidate term=2   leader=-                        servers=5
  kv-rest-55cfc4ff5-zdm9h  role=candidate term=2   leader=-                        servers=5
  kv-seed-7c4d67d96c-vp9d4 role=leader    term=2   leader=kv-seed-7c4d67d96c-vp9d4 servers=1
  http://10.244.1.8:8080   unreachable
  membership according to kv-seed-7c4d67d96c-vp9d4:
    kv-seed-7c4d67d96c-vp9d4 10.244.2.12:7000   alive
  read city on the new seed (kv-seed-7c4d67d96c-vp9d4), the pod behind Service kv-seed:
  -> 404
  stale reads on kv-rest-55cfc4ff5-tf665 (a member of the old cluster):
paris (city -> 200)
red (color -> 200)
  stale reads on kv-rest-55cfc4ff5-zdm9h (a member of the old cluster):
paris (city -> 200)
red (color -> 200)
```

Read that pod list carefully:

- The new seed `vp9d4` started with an empty `emptyDir` and `--bootstrap=true`, so it did
  exactly what it was told: it created a **new cluster of one** and elected itself leader.
  It's `1/1 Ready`. The Service `kv-seed` now sends every client and every future joiner
  to a cluster that has **none of our data** (`city -> 404`).
- The pods that *do* have the data (`tf665`, `zdm9h`) are `0/1`, not ready. They're 2 of 5
  voters in the old cluster, so they can never elect a leader again.
- The old seed `ndj5h` is still listed, shutting down. The Deployment started its
  replacement without waiting for it to exit. For a few seconds there were two pods that
  both believed they were "the seed".

From Kubernetes' point of view this is a healthy app: the seed is Ready and the Deployment
has the replicas it asked for. From the data's point of view it's gone. Every acknowledged
write still sits on two disks, but no client can read it through a leader, and the endpoint
everybody uses now answers "not found".

The planned prediction here was "raftwatch shows two leaders". It didn't: the old group
had lost its majority, so it had no leader. Instead there's **one leader, and it's the
wrong one**. That's arguably worse, because nothing looks like a conflict.

## Why it broke

Raft assumes three things that a Deployment takes away:

| Raft assumes | Deployment gives you |
|---|---|
| **Stable identity.** A server that restarts comes back as the *same* member, with the same ID and address. | A random pod name and a new pod IP on every replacement. To Raft, every restart is a new server, and the old one becomes a ghost voter. |
| **Durable state.** A member's log and term survive a restart. That's how Raft knows what it has already promised. | `emptyDir` is deleted with the pod. The replacement remembers nothing, not even that it was ever in a cluster. |
| **Bootstrap happens exactly once.** | "The seed always bootstraps" is a rule about a pod template, and the template is reused on every restart. So it bootstraps every time. |

Each of those alone is survivable for a while. Together they make the cluster degrade by one
voter per restart, until an ordinary pod deletion wedges it or splits it off into an empty
twin.

## What we changed

Nothing in this stage. The manifests are wrong on purpose and stay that way as a reference.
Stage 4 replaces both Deployments with one **StatefulSet**:

- stable pod names `kv-0`, `kv-1`, `kv-2`, used as Raft IDs;
- stable DNS names (`kv-0.kv.raftlab.svc.cluster.local`) as Raft addresses, instead of pod IPs;
- one **PersistentVolumeClaim** per pod, reattached to the replacement;
- bootstrap only by `kv-0`, and only when it has no existing Raft state.

## What we learned

- Raft membership is **explicit**. A node disappearing doesn't remove it. Every replacement
  with a new identity leaves a ghost voter that still counts toward the majority.
- "Pods are cattle" is the opposite of what a consensus member needs. It needs to be the same
  pet after every restart: same name, same address, same disk.
- Whether you lose the cluster can come down to a race between "the leader notices it lost
  its majority" and "the replacement pod joins". Never rely on a race being won.
- Kubernetes readiness can be green while the data is unreachable. A freshly bootstrapped,
  empty node is perfectly "ready".
- `504 outcome-unknown` showed up in real life on the very first run: a membership change
  that the leader couldn't confirm did, in fact, take effect.
