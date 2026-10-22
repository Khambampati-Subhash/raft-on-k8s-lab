# Stage 0 — One server, no copies

## What we tried

The simplest key-value store possible: one process, data kept in a Go map in memory,
two HTTP calls.

```
PUT /kv/<key>   body = value   → 204
GET /kv/<key>                  → 200 value, or 404
```

The code is [`cmd/naivekv/main.go`](../../cmd/naivekv/main.go). Run the demo yourself:

```bash
./scripts/stage0-demo.sh
```

## What broke

Real output:

```
== start one naivekv
PUT alpha -> 204
PUT beta -> 204
PUT gamma -> 204
GET beta -> value-of-beta
== kill -9 the server (pid 26433)
GET beta while down -> 000 (000 = connection refused)
== restart it
GET alpha -> 404
GET beta -> 404
GET gamma -> 404
```

Two separate failures:

1. **While it is down, nobody can read or write.** `000` means curl could not even connect.
2. **After it comes back, everything is gone.** The server said `204` ("saved") for all
   three keys, and then lost all of them.

## Why

- The data lived only in the process's memory. When the process died, the memory went with it.
- There was exactly one copy, on exactly one machine. One crash = total outage.

Writing to disk would fix (2) but not (1): a server that is down is down, whatever its
disk holds. And if the disk itself dies, we're back to (2).

## What we changed

The obvious idea: **keep copies of the data on more than one server.** If one dies, the
others still have it and can keep answering. That's called **replication**, and it's
stage 1.

## What we learned

- "Saved" (`204`) is a promise. A single in-memory server can't keep it past a crash.
- Availability (can I reach it?) and durability (is my data still there?) are two
  different problems. One server solves neither.
