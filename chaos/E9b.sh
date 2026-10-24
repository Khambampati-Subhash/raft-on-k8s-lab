#!/usr/bin/env bash
# E9b: wipe kv-0's disk under load (kv-0 is the one --bootstrap=auto lets bootstrap).
exec "$(dirname "$0")/../scripts/wipe-pvc.sh" 0
