#!/usr/bin/env bash
# E9a: wipe follower kv-2's disk under load.
exec "$(dirname "$0")/../scripts/wipe-pvc.sh" 2
