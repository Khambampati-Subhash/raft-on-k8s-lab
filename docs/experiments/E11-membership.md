# E11 — Changing the cluster size

Runs on a **5-worker** kind cluster (`make cluster-up-5`), because the anti-affinity rule
needs one node per member. Each run starts from the usual fresh 3-member cluster.

- **E11a** ([`chaos/E11a.sh`](../../chaos/E11a.sh)): scale 3 → 5 under load.
- **E11b** ([`chaos/E11b.sh`](../../chaos/E11b.sh)): 3 → 5, then 5 → 3 **naively**, by just
  changing `replicas`. Then repair.
- **E11c** ([`chaos/E11c.sh`](../../chaos/E11c.sh)): 3 → 5, then 5 → 3 **leave-first**:
  [`scripts/scale-down.sh`](../../scripts/scale-down.sh) asks the leader to remove `kv-4` and
  `kv-3` from the membership, and only then scales the StatefulSet.

## The arithmetic

Raft needs a **majority of the voters on the list**, alive or not:

| Voters listed | Majority | Alive | Further failures survivable |
|---|---|---|---|
| 3 | 2 | 3 | **1** |
| 5 | 3 | 5 | **2** |
| 5 (2 of them gone) | 3 | 3 | **0** |

The last row is what a naive scale-down leaves behind: three healthy pods, a cluster that
looks fine, and **no failure tolerance at all**. Any one pod restart stops all writes.

## Hypothesis

_Written before the runs._

- **E11a:** `kv-3` and `kv-4` start, ask to join, and the leader adds them as voters one at a
  time (each a log entry). **5 voters**, no election, no window, **Lost 0**.
- **E11b:** after `replicas: 3`, the membership still lists **5 voters, 2 DEAD**. Writes keep
  working (3 of 5), so loadgen sees nothing: **Lost 0**. The damage is the last row of the table
  above. The repair (`raftctl leave` for the two dead IDs) brings it back to 3 voters.
- **E11c:** leave-first ends with **exactly 3 voters, all alive**, no election, **Lost 0**.
  Each `leave` is a committed membership change, so the cluster never counts a dead member.

## Observed

### E11a: 3 → 5 ([log](results/E11a-20261024t103351.log))

```
after scale-up:
  leader kv-0, 5 voters, majority = 3, alive = 5
  writes: 201978 total, 201978 ok, 0 failed, 0 indeterminate
  unavailability windows: 0
  Lost: 0
```

### E11b: 5 → 3 naively, then repair ([log](results/E11b-20261024t103654.log))

```
at 5:
  leader kv-0, 5 voters, majority = 3, alive = 5
after naive scale-down to 3:
  leader kv-0, 5 voters, majority = 3, alive = 3
    kv-0  alive
    kv-2  alive
    kv-1  alive
    kv-4  DEAD
    kv-3  DEAD
repair: removing the dead members
kv-4 removed
kv-3 removed
  leader kv-0, 3 voters, majority = 2, alive = 3
  writes: 278727 total, 278727 ok, 0 failed, 0 indeterminate
  unavailability windows: 0
  Lost: 0
```

### E11c: 5 → 3 leave-first ([log](results/E11c-20261024t104019.log))

```
at 5:
  leader kv-0, 5 voters, majority = 3, alive = 5
kv-4 removed
kv-3 removed
after leave-first scale-down to 3:
  leader kv-0, 3 voters, majority = 2, alive = 3
  writes: 275747 total, 275747 ok, 0 failed, 0 indeterminate
  unavailability windows: 0
  Lost: 0
```

| Prediction | Held? |
|---|---|
| E11a: 5 voters, no election, no window, Lost 0 | **Yes.** |
| E11b: 5 voters listed with 2 dead; loadgen sees nothing; repair → 3 | **Yes**, exactly. |
| E11c: exactly 3 voters, all alive, no election, Lost 0 | **Yes.** |

No run had an election. After every run all commit indexes converged (`exp.sh` checks).

## Explanation

Membership in Raft is **data in the log**, not a property of what's running. Adding a voter
is a log entry the leader commits. So is removing one. Kubernetes doesn't write either:

- **Scaling up works by itself**, because a new pod asks to join (`--join`) and the leader adds
  it. That's code we wrote (stage 2's join loop), not something the StatefulSet does.
- **Scaling down does not**, because nothing tells Raft. The StatefulSet deleted `kv-3` and
  `kv-4`, and Raft kept counting them. From the outside, E11b's cluster after the naive
  scale-down looks perfect: 3 pods Ready, writes at full speed, Lost 0. Inside, it's the third
  row of the table above: **majority 3, alive 3, zero failures to spare**. The next routine pod
  restart would have stopped all writes until it came back.
- **Leave-first** makes the order right: shrink the membership (two committed log entries),
  then remove the pods. At no point does the cluster count a member that isn't there.

E11a wrote noticeably fewer writes (202k vs ~277k in E11b/E11c). It ran with 5 voters for
most of the 120 s, while E11b/E11c spent only a few seconds at 5. A leader with 4 followers
does more replication work per write, and all 6 kind nodes share one laptop.

**A trap E11b leaves behind:** a StatefulSet keeps the PVCs of pods it scaled away. If this
cluster is later scaled back to 5, `kv-3` and `kv-4` start with their **old** Raft state,
including the old membership, even though they've been removed. Deleting `data-kv-3` and
`data-kv-4` after a scale-down avoids that. ([E9](E9-wiped-pvc.md) is about what goes wrong
when you delete the wrong one.)

## Takeaway

For a Raft cluster, `kubectl scale` is only half of a membership change. Scale-down must be
**leave first, then scale**, or the cluster silently loses its failure tolerance while every
dashboard stays green.
