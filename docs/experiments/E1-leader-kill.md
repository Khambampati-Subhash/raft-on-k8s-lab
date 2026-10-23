# E1 — Kill the leader

## Hypothesis

_Written before the run._

- Writes stop for **1–3 s**: about one heartbeat timeout (1 s default) for a follower to give
  up on the dead leader, plus a vote round. loadgen shows **exactly one** unavailability
  window of roughly that size.
- A follower wins a new term (**term +1**). The killed pod comes back under the **same name**
  with its PVC and rejoins as a **follower**. The membership stays exactly `kv-0..kv-2`.
- Writes in flight on the leader when it died come back **indeterminate** (a handful at
  most). Writes sent while there's no leader are **failed** (503/refused).
- **Lost: 0.**
