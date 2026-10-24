# Chaos Mesh experiments that don't work on this cluster

Kept for reference and for anyone running the lab on **amd64 Linux**. They live outside
`chaos/` so `scripts/exp.sh` never picks them up. A stuck IOChaos finalizer would hang the
namespace reset.

| File | Problem on kind / Docker Desktop / Apple Silicon (arm64) | Evidence |
|---|---|---|
| `E7-clock-skew.yaml` (TimeChaos) | Injects "successfully", then the target's Go runtime segfaults in `runtime.nanotime`: the vDSO patch breaks Go's clock read on arm64. | [`E7-probe`](../../docs/experiments/results/E7-probe-20261024t092343.log) |
| `E8a-disk-latency.yaml`, `E8b-disk-fault.yaml` (IOChaos) | The arm64 `chaos-daemon:v2.8.6` image ships an **x86-64** `toda` binary. It can't start inside the target's mount namespace (`rosetta error: failed to open elf at /lib64/ld-linux-x86-64.so.2`). | [`E8-probe`](../../docs/experiments/results/E8-probe-20261024t092343.log) |

E8 runs instead through an in-process fault injector (`internal/node/faultstore.go`,
`chaos/E8a.sh`, `chaos/E8b.sh`). E7 is not run. See
[`docs/experiments/E7-clock-skew.md`](../../docs/experiments/E7-clock-skew.md).
