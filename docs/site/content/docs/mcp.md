---
title: "MCP server"
linkTitle: "MCP server"
weight: 20
description: "Registering the run_go_code tool with Claude Code or Gemini CLI, the tool schema, sample prompts."
---


`cmd/mcp-server` is a Model Context Protocol server that exposes one
tool — `run_go_code` — backed by `pkg/goruntime`. Any MCP client (Claude
Code, Claude Desktop, Cursor, etc.) can use it to build and execute Go
programs in an isolated Kubernetes sandbox.

This guide covers: registering the server in Claude Code, what the tool
does, a smoke-test path that doesn't require an LLM, and prompts to
try once it's wired up.

## Prerequisites

You need everything from [howto.md]({{< relref "install.md" >}}) — a Kubernetes cluster
(kind or GKE) with the agent-sandbox controller installed, the
`sandbox-router` deployed in your target namespace, and the
`go-runtime-template` `SandboxTemplate` applied. Easiest path: run
`./scripts/run-test-kind.sh` once; the script's cleanup trap removes
the per-test resources at the end, so before using the MCP server
you'll want to re-apply just the router and template:

```bash
AS_VERSION=v0.4.6
curl -sfL "https://raw.githubusercontent.com/kubernetes-sigs/agent-sandbox/${AS_VERSION}/clients/python/agentic-sandbox-client/sandbox-router/sandbox_router.yaml" \
  | sed -e "s|\${ROUTER_IMAGE}|sandbox-router:${AS_VERSION}|g" \
        -e "s|# imagePullPolicy: Never|imagePullPolicy: IfNotPresent|" \
  | kubectl -n default apply -f -
kubectl -n default rollout status deployment/sandbox-router-deployment --timeout=180s

kubectl apply -k manifests/base                       # or manifests/overlays/gvisor on GKE
kubectl apply -f manifests/warmpool.yaml              # optional but recommended — cuts cold-start
```

See [howto.md → Picking a runtime class]({{< relref "install.md" >}}#picking-a-runtime-class)
for the kustomize layout, and [howto.md → Warm pool]({{< relref "install.md" >}}#warm-pool)
for tuning the pool size.

Build the server binary:

```bash
go build -o ./bin/mcp-server ./cmd/mcp-server
```

## Registering the server in an MCP client

Templates for both Claude Code and Gemini CLI live in
[`../cmd/mcp-server/`](../cmd/mcp-server/). The `mcpServers` block is
identical between clients — they differ only in where the file needs
to land for the tool to find it.

The cross-client template (substitute the absolute path):

```json
{
  "mcpServers": {
    "go-runtime-sandbox": {
      "command": "/ABSOLUTE/PATH/TO/go-runtime-sandbox/bin/mcp-server",
      "args": [
        "--namespace=default",
        "--template=go-runtime-template"
      ]
    }
  }
}
```

### Claude Code

Working file: `<repo>/.mcp.json` (at the repo root, **not** under
`.claude/`).

```bash
cp cmd/mcp-server/claude-code.mcp.json.example .mcp.json
# edit .mcp.json: replace /ABSOLUTE/PATH/TO/ with your checkout path
```

User-global alternative:

```bash
claude mcp add go-runtime-sandbox /absolute/path/to/bin/mcp-server \
  -- --namespace=default --template=go-runtime-template
```

### Gemini CLI

Working file: `<repo>/.gemini/settings.json` (project) or
`~/.gemini/settings.json` (user). Gemini CLI's MCP block is nested
inside its broader `settings.json`, so merge rather than replace if
you already have one.

```bash
mkdir -p .gemini
cp cmd/mcp-server/gemini-cli.settings.json.example .gemini/settings.json
# edit .gemini/settings.json: replace /ABSOLUTE/PATH/TO/ with your checkout path
```

User-global alternative:

```bash
gemini mcp add go-runtime-sandbox /absolute/path/to/bin/mcp-server \
  --scope user -- --namespace=default --template=go-runtime-template
```

Both working files (`.mcp.json` and `.gemini/`) are gitignored so the
absolute path doesn't ride along with commits.

Optional flags:

| Flag             | Default                | Effect                                                                                          |
| ---------------- | ---------------------- | ----------------------------------------------------------------------------------------------- |
| `--namespace`    | `default`              | Kubernetes namespace the sandbox lives in (must also be where `sandbox-router-svc` is deployed) |
| `--template`     | `go-runtime-template`  | `SandboxTemplate` name                                                                          |
| `--claim`        | (empty)                | Reattach to an existing sandbox by claim name instead of creating one                           |
| `--persistent`   | `false`                | On client disconnect, `Disconnect` the sandbox (preserve it) instead of `Close` (delete it)     |
| `--ephemeral`    | `false`                | Wipe `/app` before every tool call (build/module caches preserved). Use when you don't want state to leak between logical operations. |
| `--open-timeout` | `5m`                   | Max time spent provisioning the sandbox on the first tool call                                  |

The server **lazily** creates the sandbox on the first `run_go_code`
call, so launching the MCP client doesn't immediately spin up a pod.
Subsequent calls reuse the same sandbox — files persist, the build
cache stays warm, binaries built earlier remain executable. When the
MCP client disconnects, the sandbox is deleted unless `--persistent`
is set.

After editing the config, restart your MCP client so it picks up the
new entry — neither Claude Code nor Gemini CLI hot-reloads MCP config
mid-session. In either client, `/mcp` shows the server status; look
for `go-runtime-sandbox` listed as `connected`.

## The tool

`run_go_code` takes two fields:

| Field     | Type                       | Required | Meaning                                                                                                                                                                                                |
| --------- | -------------------------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `files`   | `object<string, string>`   | no       | Files to write under `/app` before running the command. Keys are relative paths and may contain `/` (e.g. `"greet/greet.go"`). Existing files not in the map are left alone. Omit to run against existing state. |
| `command` | `string`                   | yes      | Shell command run via `sh -c` in `/app`. Examples: `"go run main.go"`, `"go test ./..."`, `"go build -o app . && ./app"`.                                                                                              |

Returned as a single text block:

```
exit=<N>  (<duration>)
── stdout ──
...
── stderr ──
...
```

If the command exits non-zero, the tool also sets `IsError: true` in
the MCP response so the client/agent knows to react. Long output is
head+tail truncated (the most recent error/panic survives at the tail)
with `[truncated: stdout]` or `[truncated: stderr]` marked in the
header.

## Smoke-test without an LLM

A small client in `cmd/mcp-smoke-test` spawns the server as a subprocess
and exercises four flows (single-file, build+rerun, multi-file,
compile error). Useful for validating any change you make to the
server without involving Claude.

```bash
go build -o /tmp/mcp-server ./cmd/mcp-server
go run ./cmd/mcp-smoke-test -server /tmp/mcp-server
```

Expected last lines:

```
========== multi-file build+run ==========
exit=0  (...)
── stdout ──
hi from sub-package

========== intentional compile error ==========
exit=1  (...)
── stderr ──
# command-line-arguments
./main.go:2:14: undefined: broken
(tool reported IsError=true)

smoke complete
```

## Prompts to try

Once registered in Claude Code, ask Claude to use the tool. Some prompts
that exercise the iterate-on-error loop the tool is built for:

- *"Use the run_go_code tool to write and run a Go program that prints
  the first 10 prime numbers."*
- *"Write a Go module with a `mathx` package containing a `Fib(n int)
  int` function, plus a `main.go` that prints `Fib(20)`. Add a unit
  test for `Fib`. Use run_go_code to build and run the tests."*
- *"Solve [some LeetCode-style problem] in Go. Use run_go_code to
  iterate on a solution until your test cases pass."*

The interesting behavior to watch for is iteration: the agent writes
code, sees a build error or wrong output, reads the error in the tool
response, edits a file, calls `run_go_code` again. Because state
persists across calls, it can re-upload just the file that changed.

## Watching what the server does

Two log surfaces, both stderr-only (stdout is reserved for MCP frames):

**MCP server logs** (lifecycle of the K8s claim, served from the local
subprocess). In Claude Code: `/mcp` → select the server → view logs.
Look for:

- `opening sandbox (namespace=… template=… claim="…")`
- `sandbox open: claim=<name>`
- `closing (deleting sandbox <name>)` or `disconnecting (claim <name> preserved)`

**Sandbox-side logs** (every command the agent runs, with timing).
The runtime server inside the pod emits a structured pair per call:

```
time=…  level=INFO  msg="exec begin"  command="go build -o app ."
time=…  level=INFO  msg="exec done"   command="go build -o app ." exit=0 stdout_bytes=0 stderr_bytes=0 duration_ms=287
```

Commands longer than 120 chars get clipped to `…`; newlines collapse
to a `⏎` glyph so each call is one log line. View them with:

```bash
kubectl logs -n <NS> <sandbox-pod> | grep -E 'exec '
```

To find the live sandbox while the MCP session is connected:

```bash
kubectl get sandboxclaims -n <NS>                          # claim name
kubectl get pods -n <NS> -l sandbox=go-runtime-sandbox     # the pod
kubectl exec -it -n <NS> <pod> -- sh -c 'cd /app && ls'    # filesystem inspection
```

## Troubleshooting

**Server connects but `run_go_code` calls hang for ~5 minutes.** The
first call provisions the sandbox; sandbox creation is timing out.
Almost always a cluster-side issue — `kubectl get sandboxclaims`,
`kubectl get pods`, `kubectl describe sandboxclaim <name>` will tell
you why. Common: SandboxTemplate not applied; router not deployed in
the target namespace; image pull failure on the sandbox pod.

**`502: Could not connect to the backend sandbox`** in tool output.
The `SandboxTemplate` is missing `spec.service: true`. See
[howto.md]({{< relref "install.md" >}}) troubleshooting section.

**`/mcp` shows the server `failed` or `disconnected`**. Run
`./bin/mcp-server --help` manually — if that prints help, the binary is
fine. Then run the smoke test (above) which uses the real MCP
protocol. Most often it's a `kubectl` config issue — the server uses
the same default kubeconfig discovery the demo does.

**Sandboxes leaking after restarts.** You're using `--persistent` but
not reattaching to the same claim. Either drop `--persistent`, or pass
`--claim=<name>` on the next launch. `kubectl get sandboxclaims` will
show what's still out there; `kubectl delete sandboxclaim <name>`
cleans up.
