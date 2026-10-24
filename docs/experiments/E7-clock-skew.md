# E7 — Skew the leader's clock (not run: not testable on this platform)

## Hypothesis

_Written before the probe._

Setting the leader's wall clock 5 minutes back changes **nothing**: no election, no window,
Lost 0. Raft's timers (heartbeat, election, leader lease) measure *elapsed* time, and Go
measures elapsed time with the monotonic clock, which a wall-clock change doesn't touch.

## Injection (attempted)

A Chaos Mesh `TimeChaos` with `timeOffset: "-5m"` on the leader
([`chaos/unsupported/E7-clock-skew.yaml`](../../chaos/unsupported/E7-clock-skew.yaml)), probed
first with a short duration as the plan requires.

## Observed

The injection doesn't skew the clock. It **kills the process**. From
[`results/E7-probe-20261024t092343.log`](results/E7-probe-20261024t092343.log), TimeChaos
applied *alone* to leader `kv-1`:

```
restartCount 1 -> 2 within 14s of apply; chaos record: raftlab/kv-1/kvnode phase=Injected injected=1
{"exitCode":139,"finishedAt":"2026-10-24T09:24:15Z","reason":"Error",...}
SIGSEGV: segmentation violation
PC=0xffffb680c014 m=3 sigcode=1 addr=0x18

goroutine 0 gp=0x4000003180 m=3 mp=0x4000073008 [idle]:
runtime.nanotime(...)
	runtime/time_nofake.go:33
runtime.findRunnable()
```

The first probe (TimeChaos applied together with the IOChaos probes) crashed the same way.

TimeChaos works by rewriting the target process's **vDSO**, the small piece of kernel code
mapped into every process that answers `clock_gettime` without a system call. Go's runtime
reads its clocks through the vDSO directly (`runtime.nanotime` above), and on arm64 the
patched code faults on the first read (`addr=0x18`). The chaos object still reports
`Injected`.

**Fallbacks considered and rejected:**

- **libfaketime + `LD_PRELOAD`** (the plan's fallback) intercepts libc's `clock_gettime`. Go
  doesn't call libc for time, even in a dynamically linked cgo build: it calls the vDSO
  itself. The preload would load and change nothing.
- **Changing the node's clock**: every kind node is a container on the *same* Docker VM
  kernel, so this skews all three Raft members at once. That isn't a skew between them.
- **Time namespaces**: Linux time namespaces can offset `CLOCK_MONOTONIC` and
  `CLOCK_BOOTTIME`, but not `CLOCK_REALTIME`, which is exactly the clock we wanted to move.

## Explanation (code reading, not an experiment)

The hypothesis can still be checked against the source. These are hashicorp/raft v1.8.0's
timers, from the module cache:

```go
// util.go: every heartbeat / election timeout
func randomTimeout(minVal time.Duration) <-chan time.Time {
	extra := time.Duration(rand.Int63()) % minVal
	return time.After(minVal + extra)
}

// raft.go, runFollower: "have I heard from the leader recently?"
lastContact := r.LastContact()
if time.Since(lastContact) < hbTimeout {

// raft.go, checkLeaderLease: "has a majority answered me recently?"
now := time.Now()
diff := now.Sub(f.LastContact())
if diff <= leaseTimeout {

// replication.go
func (s *followerReplication) setLastContact() {
	s.lastContact = time.Now()
```

Every one of these is either a runtime timer (`time.After`) or a subtraction of two
`time.Time` values that both came from `time.Now()`. Since Go 1.9, `time.Now()` carries a
**monotonic clock reading** alongside the wall time, and `Sub`/`Since` use the monotonic
reading when both sides have one. Moving the wall clock 5 minutes back changes the wall part
of every `time.Time` and leaves every difference Raft computes unchanged.

So by code reading, the hypothesis holds for hashicorp/raft: leader election, heartbeats and
leases are immune to wall-clock skew. **What wasn't tested** is anything that compares wall
times *across* nodes. Raft itself doesn't, but an application that, say, used leader-stamped
timestamps for TTLs would be exposed.

## Takeaway

On this platform the chaos tool fails before Raft gets a chance to. It also means "the chaos
object says Injected" is not evidence that a fault did what it says, which is why every
experiment here checks pod restarts and what actually happened. Running E7 for real needs an
amd64 Linux cluster.
