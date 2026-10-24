# Glossary

Plain-English definitions, added as each term first shows up in the journey.

| Term | Meaning |
|---|---|
| **Replica** | One copy of the data, held by one server. |
| **Replication** | Keeping several replicas so that losing one server doesn't lose the data. |
| **Consistency** | Whether all replicas (and all clients reading them) see the same data. |
| **Split brain** | Two parts of a system each believe they are in charge and accept conflicting writes. |
| **Consensus** | Getting a group of machines to agree on one value, or one order of writes, even when some of them crash or can't talk to each other. |
| **Raft** | A consensus algorithm: a cluster elects one leader, the leader orders all writes in a log, and a write is safe once a majority stores it. |
| **Leader** | The one node (per term) that accepts writes and decides their order. |
| **Follower** | A node that copies the leader's log and redirects clients to the leader. |
| **Candidate** | A follower that stopped hearing from the leader and is asking for votes. |
| **Term** | An election round, numbered 1, 2, 3, … At most one leader per term. A higher term always wins. |
| **Log** | The append-only, ordered list of commands every node keeps on disk. |
| **Commit / committed** | A log entry is committed once a majority has stored it. Committed entries are never lost. |
| **Majority / quorum** | More than half of the voting members: 2 of 3, 3 of 5. Needed to elect a leader and to commit. |
| **Heartbeat** | Small regular message from the leader that says "I'm still here." |
| **Election timeout** | How long a follower waits without a heartbeat before starting an election. Randomized so nodes don't all start at once. |
| **State machine (FSM)** | The thing the log is applied to. Here, a Go map. |
| **Stale read** | Reading a node's local copy without checking with the leader. Fast, but may miss recent writes. A partitioned node serves arbitrarily old data this way (E3: 40 s), and a freshly restarted one can briefly serve an empty store (E1). |
| **Linearizable read** | A read that reflects every write acknowledged before it started. Here: the leader confirms it's still leader with a majority before answering. |
| **Bootstrap** | Creating a brand-new cluster: the very first node declares itself the only member. Must happen exactly once. |
| **Voter / membership** | The list of servers Raft counts when it computes a majority. It changes only through explicit "add voter" / "remove server" entries in the log, never by a node just disappearing. |
| **Ghost voter** | A member that is still in the membership list but will never come back (its pod and identity are gone). It still counts toward the majority. |
| **Pre-vote** | A dry-run election: a candidate first asks "would you vote for me?" without bumping the term, so a node that can't win doesn't disrupt the cluster. hashicorp/raft does this, which is why a stuck candidate's term doesn't climb. |
| **Deployment / StatefulSet** | Kubernetes controllers that keep N pods running. A Deployment's pods are interchangeable: random names, new IP, fresh disk on every replacement. A StatefulSet's pods have stable names (`kv-0`), stable DNS and their own persistent disk. |
| **emptyDir / PVC** | Pod storage. An `emptyDir` is deleted with the pod. A PersistentVolumeClaim outlives the pod and is reattached to its replacement. |
| **Partition** | The network splits the cluster into groups that can't reach each other, while every node keeps running. The group with a majority carries on; the minority can't elect a leader or commit. |
| **Leader lease** | A leader that hasn't heard from a majority for `LeaderLeaseTimeout` (500 ms default in hashicorp/raft) steps down on its own, so a cut-off leader stops accepting writes quickly. |
| **Acknowledged write** | A write the client was told succeeded (`2xx`). The one promise this lab checks: it must still be there afterwards. |
| **Indeterminate / outcome-unknown** | A write whose result the client can't know: a timeout, a connection cut after sending, or `504 outcome-unknown` from a leader that lost leadership mid-write. It may or may not have been applied (E5b: 16 of 20 had been). Never "failed", never "ok". |
| **Lost write** | An acknowledged write that's missing or wrong when read back through the leader. loadgen's headline number; must be 0. Only E9's wiped disks produced any. |
| **Unavailability window** | A stretch of more than 500 ms with no acknowledged write (loadgen's measure of an outage). |
| **Snapshot / log compaction** | A copy of the whole state machine at some log index, after which the log entries before it are deleted. hashicorp/raft checks whether to snapshot only every 120 s, so a busy log grows large in between (E1's OOM). |
| **Pipeline replication** | The leader sends AppendEntries to a follower without waiting for each reply. Faster, and in hashicorp/raft v1.8.0 the source of a deadlock when a follower rejects an append (E8b). |
| **Non-voter** | A member that receives the log but doesn't count toward the majority or vote. The safe way to bring back a node that lost its disk (E9); not implemented here. |
| **Liveness / readiness probe** | Kubernetes health checks. Readiness failing: send no traffic. Liveness failing: kill and restart the container. Liveness must never depend on "is there a leader" (stage 4). |
| **PodDisruptionBudget (PDB)** | Limits how many pods voluntary operations (drains, rollouts) may take down at once. Here `maxUnavailable: 1`. |
| **Headless Service** | A Service without a virtual IP that gives every pod its own DNS name (`kv-0.kv.raftlab.svc.cluster.local`), which is what the Raft transport uses as an address. |
| **Monotonic clock** | A clock that only moves forward and isn't affected by setting the wall-clock time. Go uses it for durations, and so do all of Raft's timers (E7). |
| **vDSO** | A small piece of kernel code mapped into every process so it can read the clock without a system call. Chaos Mesh's TimeChaos patches it, which crashes Go on arm64 (E7). |
| **Chaos Mesh** | The fault-injection tool used for the experiments (pod kills, network partitions, latency, loss), installed into the kind cluster with Helm. |
