# raft-on-k8s-lab

> **Learning project — not production software.**

A hands-on lab for seeing how Raft consensus behaves when real-world failures hit a
Kubernetes cluster. We import [`hashicorp/raft`](https://github.com/hashicorp/raft),
wrap it in a tiny key-value service, run it on `kind`, and break it on purpose.

Start reading at [`docs/journey/`](docs/journey/).
