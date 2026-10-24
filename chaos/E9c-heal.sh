#!/usr/bin/env bash
# Safety net: remove E9c's NetworkChaos.
kubectl -n raftlab delete networkchaos e9c-dark-majority --ignore-not-found --timeout=60s >/dev/null 2>&1
