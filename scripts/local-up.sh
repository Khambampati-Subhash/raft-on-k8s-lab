#!/usr/bin/env bash
# Stage 2: three kvnodes on this machine. n1 bootstraps; n2 and n3 join it.
#   n1: raft 127.0.0.1:7001  http 127.0.0.1:8081   (n2: 7002/8082, n3: 7003/8083)
# Data and logs live under $RAFTLAB_DIR (default ${TMPDIR:-/tmp}/raftlab).
set -euo pipefail
cd "$(dirname "$0")/.."
make -s build
DIR=${RAFTLAB_DIR:-${TMPDIR:-/tmp}/raftlab}
mkdir -p "$DIR"

start() { # start <i> [extra flags...]
  local i=$1; shift
  nohup ./bin/kvnode --id n$i --http 127.0.0.1:808$i --raft-bind 127.0.0.1:700$i \
    --raft-advertise 127.0.0.1:700$i --data "$DIR/n$i" "$@" >>"$DIR/n$i.log" 2>&1 &
  echo $! > "$DIR/n$i.pid"
}

start 1 --bootstrap=true
start 2 --bootstrap=false --join=http://127.0.0.1:8081
start 3 --bootstrap=false --join=http://127.0.0.1:8081

for i in 1 2 3; do
  for _ in $(seq 1 100); do
    [ "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:808$i/readyz)" = 200 ] && break
    sleep 0.1
  done
  printf "n%s ready: %s\n" $i "$(curl -s http://127.0.0.1:808$i/status)"
done
echo "logs: $DIR/n{1,2,3}.log"
