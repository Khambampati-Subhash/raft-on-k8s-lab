#!/usr/bin/env bash
# Stop the local cluster and delete its data.
DIR=${RAFTLAB_DIR:-${TMPDIR:-/tmp}/raftlab}
for f in "$DIR"/n*.pid; do [ -f "$f" ] && kill "$(cat "$f")" 2>/dev/null; done
sleep 0.5
rm -rf "$DIR"
echo "local cluster stopped, $DIR removed"
