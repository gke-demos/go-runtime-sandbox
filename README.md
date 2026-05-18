# go-runtime-sandbox

A proof of concept that ships arbitrary Go source code into a
[Kubernetes Agent Sandbox][as] from a host program, compiles and
executes it inside the sandboxed pod, and returns the result. Built
for the AI-agent use case: an LLM writes Go code, calls a tool, sees
the output, iterates.

[as]: https://github.com/kubernetes-sigs/agent-sandbox

```
┌────────────────┐   stdio MCP    ┌──────────────────┐   port-forward   ┌────────────────────┐
│  Any MCP       │ ────────────▶ │  cmd/mcp-server  │ ──────────────▶ │  sandbox pod (gVisor)  │
│  client        │   run_go_code  │  (pkg/goruntime) │     HTTP :8888   │  server + Go toolchain │
└────────────────┘                └──────────────────┘                  └────────────────────┘
```

Runs on local kind (no isolation) or GKE Autopilot/Standard with the
agent-sandbox addon (gVisor isolation, validated).

## What's in the box

| Component              | What it is                                                                                                              |
| ---------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| `server/`              | HTTP server that implements the agent-sandbox wire contract (`/execute`, `/upload`, `/download`, `/list`, `/exists`)    |
| `Dockerfile`           | Three-stage runtime image (~502 MB) bundling the server + Go toolchain + pre-warmed `$GOCACHE`                          |
| `pkg/goruntime`        | Go library wrapping the agent-sandbox client with `Open`/`Execute`/`Reset`/`Close`. Handles multi-file via tar streaming |
| `pkg/format`           | LLM-friendly `Result → string` formatter (exit-prominent, head+tail truncation)                                         |
| `cmd/demo`             | Thin CLI wrapper around `pkg/goruntime` — runs a smoke flow and a multi-file flow                                       |
| `cmd/mcp-server`       | stdio MCP server exposing one `run_go_code` tool. See [`cmd/mcp-server/README.md`](./cmd/mcp-server/README.md)          |
| `cmd/mcp-smoke-test`        | Programmatic MCP client that spawns the server and exercises the tool — no LLM needed                                   |
| `manifests/`           | Kustomize bases + overlays (`base`, `gvisor`, `gke-gvisor`) and a `SandboxWarmPool`                                     |
| `_examples/`           | Sample Go programs that the demo embeds and ships into the sandbox                                                      |
| `scripts/run-test-kind.sh` | One-shot kind end-to-end: build images, install controller, deploy, run demo, clean up                              |

## Quick start (local kind)

```bash
./scripts/run-test-kind.sh
```

Builds everything, brings up a kind cluster with the agent-sandbox
controller + router, applies the template, runs `cmd/demo --flow=all`,
and confirms both single-file and multi-file Go programs execute
inside the sandbox.

For GKE, see [`docs/howto.md`](./docs/howto.md) — uses the addon
(`--enable-agent-sandbox` flag) and Artifact Registry.

## Quick start (MCP, for an agent)

Get the `mcp-server` binary — either grab a pre-built one from the
[latest release][rel] (no Go toolchain required):

```bash
# Linux x86_64 — change the suffix for darwin-amd64, darwin-arm64, linux-arm64, windows-amd64
curl -fL -o ./bin/mcp-server \
  https://github.com/gke-demos/go-runtime-sandbox/releases/latest/download/mcp-server-linux-amd64
chmod +x ./bin/mcp-server
```

…or build from source:

```bash
go build -o ./bin/mcp-server ./cmd/mcp-server
```

[rel]: https://github.com/gke-demos/go-runtime-sandbox/releases/latest

Ready-made config samples for both Claude Code and Gemini CLI live in
[`cmd/mcp-server/`](./cmd/mcp-server/). Pick the one that matches your
client:

```bash
# Claude Code (project-level)
cp cmd/mcp-server/claude-code.mcp.json.example .mcp.json

# Gemini CLI (project-level)
mkdir -p .gemini && cp cmd/mcp-server/gemini-cli.settings.json.example .gemini/settings.json
```

Edit the `command` path in whichever you copied so it points at your
local `bin/mcp-server`, then restart the client. Both working files
are gitignored.

Once connected, ask the agent to *"use the run_go_code tool to write
and run a Go program that prints the first 20 Fibonacci numbers."*
See [`cmd/mcp-server/README.md`](./cmd/mcp-server/README.md) for
client-specific registration details and
[`docs/mcp-howto.md`](./docs/mcp-howto.md) for the full operational
guide.

## Docs

User-facing docs are published at
**<https://gke-demos.github.io/go-runtime-sandbox/>**. The source lives
under [`docs/site/`](./docs/site/) (Hugo + Docsy) and covers:

- **Quick start** — local kind in under five minutes
- **Install** — kind walkthrough, full GKE recipe (agent-sandbox addon,
  Artifact Registry), runtime-class overlays, warm-pool tuning,
  troubleshooting, field notes from real-world testing
- **MCP server** — registering `run_go_code` with Claude Code or
  Gemini CLI, the tool schema, sample prompts
- **How it works** — design and rationale (three execution layers,
  multi-file upload mechanism, lifecycle decisions)

Engineering artifacts (the original spec and design docs, kept for
historical context):

- [`docs/spec.md`](./docs/spec.md) — what we set out to build
- [`docs/design.md`](./docs/design.md) — how we decided to build it

Reference docs that live with the code:

- [`cmd/mcp-server/README.md`](./cmd/mcp-server/README.md) — flags,
  tool schema, lifecycle for the MCP server binary
- [`AGENTS.md`](./AGENTS.md) — agent-coding-assistant guide

## Status

**Proof of concept.** Validated end-to-end on:

- kind 0.31 + Kubernetes 1.34 (no sandbox runtime)
- GKE Autopilot 1.36.0-gke.1759000 + the agent-sandbox addon (gVisor
  confirmed active via `runtimeClassName=gvisor`)

The MCP server has been driven by Claude Code against real GKE; it
iterates, builds, runs, and surfaces compile errors back to the model.

Not yet:

- Authentication on the MCP server (currently anyone with stdio access)
- Persistent storage for the build cache across sandbox lifecycles
  (a PVC mounted at `$GOCACHE` would make this near-instant)
- cgo support in the runtime image (no `gcc` — pure-Go workloads only)
- Gateway-mode connectivity for the agent-sandbox client (port-forward only)

Each of these is a natural follow-up if the PoC graduates.

## License / attribution

This repo depends on, but doesn't fork, [kubernetes-sigs/agent-sandbox][as].
The sandbox-router image referenced by the deployment scripts is
built straight from the upstream repo via Docker's git-context.
