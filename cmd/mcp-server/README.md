# cmd/mcp-server

A [Model Context Protocol][mcp] server (stdio transport) that exposes
one tool, `run_go_code`. The tool writes Go source files into a
Kubernetes Agent Sandbox and runs a shell command there, returning
exit code, stdout, and stderr.

[mcp]: https://modelcontextprotocol.io/

This is the "agent-facing" surface of the project. For operational
guidance (registering in Claude Code or Gemini CLI, running prompts,
troubleshooting), see the [published MCP server page][mcp-page] or
its source at [`../../docs/site/content/docs/mcp.md`](../../docs/site/content/docs/mcp.md).

[mcp-page]: https://gke-demos.github.io/go-runtime-sandbox/docs/mcp/

## Usage

```bash
go build -o ./bin/mcp-server ./cmd/mcp-server
./bin/mcp-server --help
```

The server is meant to be launched by an MCP client over stdio (Claude
Code, Gemini CLI, Cursor, etc.); running it manually just leaves it
waiting for JSON-RPC frames on stdin.

## Registering with an MCP client

Two ready-to-use samples live next to this file. Both have the same
shape (the `mcpServers` key is shared across clients); they differ
only in where the file needs to land for the tool to find it.

### Claude Code

Sample: [`claude-code.mcp.json.example`](./claude-code.mcp.json.example).
Copy it to the repo root as `.mcp.json` and edit the `command` path:

```bash
cp cmd/mcp-server/claude-code.mcp.json.example .mcp.json
# edit .mcp.json: replace /ABSOLUTE/PATH/TO/ with your checkout path
```

Restart Claude Code in the repo directory; accept the workspace-trust
prompt for the new server. `/mcp` should show `go-runtime-sandbox` as
connected.

For user-global (all projects):

```bash
claude mcp add go-runtime-sandbox /absolute/path/to/bin/mcp-server \
  -- --namespace=default --template=go-runtime-template
```

### Gemini CLI

Sample: [`gemini-cli.settings.json.example`](./gemini-cli.settings.json.example).
Gemini CLI reads from `.gemini/settings.json` (project) or
`~/.gemini/settings.json` (user) — the `mcpServers` block is the same
either way:

```bash
mkdir -p .gemini
cp cmd/mcp-server/gemini-cli.settings.json.example .gemini/settings.json
# edit .gemini/settings.json: replace /ABSOLUTE/PATH/TO/ with your checkout path
```

Or for user scope:

```bash
gemini mcp add go-runtime-sandbox /absolute/path/to/bin/mcp-server \
  --scope user -- --namespace=default --template=go-runtime-template
```

Restart Gemini CLI to pick up the new entry. `/mcp` shows status.

Both working files (`.mcp.json` and `.gemini/settings.json`) are
gitignored at the repo root — they hold per-machine absolute paths.

## Flags

| Flag             | Default                | Effect                                                                                                                                        |
| ---------------- | ---------------------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| `--namespace`    | `default`              | Kubernetes namespace the sandbox lives in (must also be where `sandbox-router-svc` is deployed)                                               |
| `--template`     | `go-runtime-template`  | `SandboxTemplate` name                                                                                                                        |
| `--claim`        | (empty)                | Reattach to an existing sandbox by claim name instead of creating one                                                                         |
| `--persistent`   | `false`                | On client disconnect, `Disconnect` the sandbox (preserve it) instead of `Close` (delete it)                                                   |
| `--ephemeral`    | `false`                | Wipe `/app` before every tool call (build/module caches preserved). Use when you don't want state to leak between logical operations           |
| `--open-timeout` | `5m`                   | Max time spent provisioning the sandbox on the first tool call                                                                                |

## The tool

```json
{
  "name": "run_go_code",
  "input_schema": {
    "type": "object",
    "properties": {
      "files":   { "type": "object", "additionalProperties": { "type": "string" } },
      "command": { "type": "string" }
    },
    "required": ["command"]
  }
}
```

- `files` (optional) — path → contents map. Paths may contain `/`
  (e.g. `"greet/greet.go"`). Files not listed are left alone; `/app`
  state persists across calls.
- `command` (required) — runs via `sh -c` in `/app`.

Output is a single text block:

```
exit=<N>  (<duration>)
── stdout ──
…
── stderr ──
…
```

Non-zero exit also sets `IsError: true` in the MCP response so the
agent knows to react. Long output is head+tail truncated (the most
recent error / panic survives at the tail) with `[truncated: stdout]`
or similar in the header.

## Lifecycle

1. Server launches → no sandbox is created yet.
2. First `run_go_code` call → server `Open`s the sandbox (claim from
   warm pool if available, otherwise cold-start). Logs `sandbox open:
   claim=<name>` to stderr.
3. Subsequent calls reuse the same sandbox. Files persist, the build
   cache stays warm, binaries built earlier remain executable.
4. MCP client disconnects → server `Close`s (delete) the sandbox.
   With `--persistent`, server `Disconnect`s instead and the sandbox
   survives for reattach.

Concurrent tool calls are serialized inside the server (a per-session
mutex around `Execute`), so two parallel invocations can't clobber
each other's files or build artifacts.

## Logs

Two streams, both stderr (stdout carries MCP frames):

**Server lifecycle** (this binary):

```
mcp-server: opening sandbox (namespace=… template=… claim="")
mcp-server: sandbox open: claim=sandbox-claim-abc12
mcp-server: closing (deleting sandbox sandbox-claim-abc12)
```

**Per-command, from the sandbox-side server** (`server/` in this repo,
running inside the pod). Visible via `kubectl logs -n <NS> <pod>`:

```
time=…  level=INFO  msg="exec begin"  command="go build -o app ."
time=…  level=INFO  msg="exec done"   command="go build -o app ." exit=0 stdout_bytes=0 stderr_bytes=0 duration_ms=287
```

## Related

- [`pkg/goruntime`](../../pkg/goruntime) — the library this binary
  wraps; can be embedded in any other agent-tool runner.
- [`cmd/mcp-smoke-test`](../mcp-smoke-test) — a programmatic MCP client that
  spawns this server and exercises the tool without involving an LLM.
  Useful for validating changes to either side.
- [Published MCP server docs][mcp-page] — registration in Claude
  Code and Gemini CLI, prompts to try, troubleshooting.
