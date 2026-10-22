# Stage 1 — Three copies, no agreement

## What we tried

Same naive server, three of them. Each one, when a client writes to it, saves the value
and then **copies the write to the other two in the background**, without waiting and
without checking whether the copy arrived ("fire and forget").

```bash
naivekv --http :8081 --peers http://127.0.0.1:8082,http://127.0.0.1:8083
```

The copy is just another `PUT` with `?replica=1`, so the receiver doesn't copy it again.

Run it yourself:

```bash
./scripts/stage1-demo.sh
```

## What broke

### (a) A node that was down misses writes forever

```
== (a) kill node 8083, write k1 to node 8081, restart 8083
PUT k1 on 8081 -> 204
GET k1 on 8081 -> 200
GET k1 on 8082 -> 200
GET k1 on 8083 -> 404
```

Node 8083 was down when `k1` was written. The copy sent to it failed, was logged, and
was thrown away. When 8083 came back, nobody told it what it missed. It will answer
`404` for `k1` forever.

### (b) Two writes at once → nodes disagree

Each round, one client writes `x=A<n>` to node 8081 while another writes `x=B<n>` to
node 8082, at the same moment. Then we ask all three nodes what `x` is:

```
round  1: 8081=B1   8082=A1   8083=B1   agree=no
round  2: 8081=B2   8082=A2   8083=A2   agree=no
round  3: 8081=B3   8082=A3   8083=B3   agree=no
round  4: 8081=B4   8082=A4   8083=A4   agree=no
round  5: 8081=B5   8082=B5   8083=B5   agree=yes
round  6: 8081=B6   8082=B6   8083=B6   agree=yes
round  7: 8081=B7   8082=A7   8083=A7   agree=no
round  8: 8081=B8   8082=A8   8083=B8   agree=no
round  9: 8081=B9   8082=A9   8083=B9   agree=no
round 10: 8081=B10  8082=A10  8083=A10  agree=no
rounds where nodes disagree: 8 / 10
```

Look at round 1: node 8081 received `A1` from a client, yet ends up holding `B1`. Node
8082 received `B1`, yet ends up holding `A1`. Each saved its own write first, and the
other node's copy arrived a moment later and overwrote it. They **swapped** values.
Node 8083 got both copies and kept whichever arrived last.

Now three clients reading `x` from three nodes get two different answers, and none of
the nodes will ever fix it.

## Why

There are two missing ideas:

1. **No agreed order.** Each node applies writes in the order *it* happens to receive
   them. Different nodes receive them in different orders, so they end up in different
   states. To stay identical, every node must apply the **same writes in the same order**.
2. **No catch-up.** A node that missed something has no way to learn what it missed.
   There's no shared history it can compare against.

Fixing (1) means someone has to decide the order, and everyone else has to accept it,
even when some nodes are down or slow. Getting a group of machines to agree on
something despite failures is called **consensus**.

"Just pick a leader to decide the order" sounds easy, but it raises new questions right
away. Who is the leader? What if it dies? What if two nodes both think they're the
leader (**split brain**)? When is a write safely "saved"? Consensus algorithms answer
exactly these questions.

## What we changed

We stop inventing and use **Raft**, a consensus algorithm designed to be
understandable, through the battle-tested [`hashicorp/raft`](https://github.com/hashicorp/raft)
library. Raft gives us:

- one **leader** at a time that decides the order of writes,
- a **log**, an ordered list of writes that every node keeps, which is what lets a lagging
  node catch up,
- the rule that a write counts as saved only once a **majority** of nodes have it.

That's stage 2.

## What we learned

- Copying data isn't enough. The copies must agree, and agreement needs an order.
- A `204` from one node told the client nothing about what the other nodes hold.
- Fire-and-forget replication fails silently. Nothing errored. The data was just wrong.
