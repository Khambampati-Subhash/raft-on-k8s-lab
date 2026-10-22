# Stage 2 — Raft on one laptop

## What we tried

Stage 1 showed that copies need an **agreed order** of writes. Instead of inventing one,
we import [`hashicorp/raft`](https://github.com/hashicorp/raft), the Raft library used
in production by Consul, Nomad and Vault, and wrap it in a small service, `kvnode`.

How the pieces fit:

```
client ──HTTP──▶ httpapi ──▶ node (wraps raft.Raft) ──▶ fsm (a Go map)
                                 │
                                 └──TCP :700x──▶ the other two nodes
```

- [`internal/fsm`](../../internal/fsm/fsm.go) is the **state machine**: a map that
  applies `set` and `delete` commands. It never talks to the network.
- [`internal/node`](../../internal/node/node.go) hands every write to Raft and only
  answers "done" once Raft says the write is **committed**.
- [`internal/httpapi`](../../internal/httpapi/server.go) turns that into HTTP.

Three processes on one machine:

```bash
make local-up                 # n1 bootstraps, n2 and n3 ask n1 to join
./scripts/stage2-demo.sh
make local-down
```

## What happened

This time nothing broke. That's the point of the stage. Here's the real output, step by step.

### One leader, everyone agrees on the term

```
== cluster
  n1: role=leader    term=2 leader=n1  commit=4
  n2: role=follower  term=2 leader=n1  commit=4
  n3: role=follower  term=2 leader=n1  commit=4
leader is n1
```

At any moment, a Raft cluster has at most one **leader** per **term**. A term is an
election round, numbered 1, 2, 3, … Only the leader accepts writes. The others are
**followers**.

### Writing to a follower gets you redirected

```
HTTP/1.1 421 Misdirected Request
X-Raft-Leader: n1
PUT on leader n1 -> 204
```

A follower doesn't accept the write. It says "not me, ask `n1`". This is how stage 1's
problem disappears: **all writes go through one node, so there is one order.**

### "Acknowledged" doesn't mean "visible everywhere yet"

```
== stale read on every node, immediately (no leader check)
  n1: paris
  n2: 404 page not found
  n3: 404 page not found
== stale read on every node, 200 ms later
  n1: paris
  n2: paris
  n3: paris
```

The leader returned `204` only after a **majority** (2 of 3) had stored the write in
their **log**, an append-only list of commands kept on disk. So the write was safe. But a
follower only applies an entry to its map once it learns the entry is **committed**, and
it learns that from the leader's *next* message. For a few milliseconds, followers are
behind.

Reading a follower's map directly (`?stale=true`) is fast, but it can return old data.
That's a **stale read**. A normal read (no `stale`) goes to the leader, which first
confirms with a majority that it's still the leader. That's a **linearizable read**: it
always reflects every write acknowledged before it:

```
== linearizable read on a follower
  n2 -> 421 (421 = ask the leader)
```

### Kill the leader

```
== kill -9 leader n1
new leader n2 elected after 2816 ms
  n1: unreachable
  n2: role=leader    term=3 leader=n2  commit=6
  n3: role=follower  term=3 leader=n2  commit=6
```

What happened in those ~2.8 seconds:

1. The leader sends a **heartbeat** to followers many times a second. When n1 died, the
   heartbeats stopped.
2. After a randomized **election timeout** (about 1–2 s with default settings), a
   follower decides the leader is gone, becomes a **candidate**, increments the term
   (2 → 3) and asks the others for votes.
3. A candidate wins with votes from a majority. Two of three nodes are alive, so the
   remaining two can still form a majority. n2 won term 3.

A node only votes for a candidate whose log is at least as up to date as its own, so the
new leader is guaranteed to have every committed write:

```
== read city through the new leader
paris  -> 200
== writes still work with 2 of 3 nodes
  PUT city=rome on n2 -> 204
```

### Bring the old leader back

```
== restart n1: it rejoins as a follower and catches up
  n1: role=follower  term=3 leader=n2  commit=8
  n2: role=leader    term=3 leader=n2  commit=8
  n3: role=follower  term=3 leader=n2  commit=8
  stale read of city on n1: rome
```

n1 restarted with its log still on disk. It saw term 3 > its own term 2, accepted n2 as
leader, and the leader sent it the entries it missed (`city=rome`). This is the
**catch-up** that stage 1's naive replication didn't have.

## Why it works

Raft's core rules, each one fixing a stage-1 failure:

| Stage 1 problem | Raft rule |
|---|---|
| Nodes applied writes in different orders | One leader per term appends every write to a single log. Everyone applies the log in the same order. |
| A write was "saved" on one node only | A write is committed only once a majority has it in their log. |
| A node that was down never caught up | The leader sends each follower whatever part of the log it's missing. |
| Two nodes could both think they're in charge | A leader needs a majority's votes. Two majorities of the same cluster always overlap, so two leaders can't win the same term. |

The price: with 3 nodes, the cluster survives **1** failure. With 2 of 3 down, there's no
majority, so no leader and no writes. With 5 nodes it survives 2. That's why clusters
have an odd number of nodes: going from 3 to 4 adds no extra failure tolerance.

## What we changed

Nothing yet. This works on one machine, where addresses never change and disks never
vanish. Stage 3 moves it into Kubernetes, where both happen all the time.

## What we learned

- Raft turns "copies that drift apart" into "copies of one ordered log."
- Leader failover is automatic but not instant. It took ~2–3 s here. During that window,
  writes fail.
- "Committed" (safe on a majority) and "visible on every node" are different moments.
  Stale reads can see the gap.
- Majority is the magic number for everything: votes, commits, and how many failures you
  can survive.
