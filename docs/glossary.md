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
| **Stale read** | Reading a node's local copy without checking with the leader. Fast, but may miss recent writes. |
| **Linearizable read** | A read that reflects every write acknowledged before it started. Here: the leader confirms it's still leader with a majority before answering. |
| **Bootstrap** | Creating a brand-new cluster: the very first node declares itself the only member. Must happen exactly once. |
