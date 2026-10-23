# E2 — Kill a follower

## Hypothesis

_Written before the run._

- **No leader change, no term change.** 2 of 3 is still a majority, and the leader keeps
  committing with the other follower.
- **No unavailability window** over 500 ms, and no failed or indeterminate writes. Clients only
  talk to the leader, which never stopped.
- The killed follower comes back under the same name with its PVC, and the leader sends it
  the entries it missed. loadgen only reports the **max** stale-read lag per node, so the
  prediction is: the killed follower's max lag is large (it fell behind while down), and the
  surviving follower's stays small (a few counter values at most).
- **Lost: 0.**
