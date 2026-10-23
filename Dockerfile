# One image holds every binary: kvnode, loadgen, raftwatch, raftctl, naivekv.
#
# Binaries are cross-compiled on the host by `make image` (pure Go, CGO off,
# -trimpath), not in a golang builder stage: the output is the same, it reuses
# the host's module and build cache, and it needs no network inside the build
# (which breaks behind TLS-intercepting proxies).
FROM gcr.io/distroless/static-debian12:nonroot
COPY bin/linux/ /
USER 65532:65532
ENTRYPOINT ["/kvnode"]
