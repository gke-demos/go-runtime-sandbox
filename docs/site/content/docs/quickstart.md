---
title: Quick start
linkTitle: Quick start
weight: 5
description: From zero to a running sandbox on a local kind cluster in under five minutes.
---

The fastest path to a working sandbox is the one-shot kind script. It builds the runtime image, brings up a kind cluster, installs the agent-sandbox controller and router, deploys the `SandboxTemplate`, and runs the demo end-to-end.

## Prerequisites

| Tool       | Version  |
| ---------- | -------- |
| Docker     | 20.10+   |
| Go         | 1.26+    |
| `kubectl`  | 1.30+    |
| `kind`     | 0.20+    |

## Run it

From the repo root:

```bash
./scripts/run-test-kind.sh
```

Expected output (tail):

```
=== single-file smoke flow ===
-- go run main.go [exit=0] --                                hello from the sandbox! sum(1..100)=5050, go=go1.26.3/linux/amd64
-- ./app (re-runs the binary built in the previous call) -- hello from the sandbox! ...

=== multi-file module flow ===
-- ./app [exit=0] --                                         hello, Alice — from the multi-file sandbox sample
                                                             hello, Bob — ...
                                                             hello, Carol — ...
-- go test ./... [exit=0] --                                 ok  example.com/multifile/greet  0.004s

==> PoC complete
```

That confirms three things end-to-end: the runtime image builds and runs, the agent-sandbox client can ship files into it via the router, and arbitrary Go programs (including multi-file modules) build and execute inside.

## What just happened

The script:

1. Creates a kind cluster named `agent-sandbox-poc` (reused on subsequent runs).
2. Installs the upstream agent-sandbox controller via `kubectl apply -f` against the v0.4.6 release manifests.
3. Builds the `sandbox-router` image (Docker pulls the source straight from `github.com/kubernetes-sigs/agent-sandbox` via git-context).
4. Builds this repo's runtime image and loads both into the kind cluster.
5. Applies `manifests/base` (the kustomize base — no `runtimeClassName` since kind has no sandbox runtime).
6. Runs `go run ./cmd/demo --flow=all` against the cluster.

## Next steps

- [Install]({{< relref "install.md" >}}) — same thing on GKE, with the addon and Artifact Registry.
- [MCP server]({{< relref "mcp.md" >}}) — register the server with Claude Code or Gemini CLI and drive it from an LLM.
- [How it works]({{< relref "how-it-works.md" >}}) — the design and the rationale for non-obvious choices.

To tear the kind cluster down entirely:

```bash
kind delete cluster --name agent-sandbox-poc
```
