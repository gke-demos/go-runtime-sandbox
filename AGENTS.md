# Agent-instruction guide for go-runtime-sandbox

A reference for any LLM coding assistant (Claude Code, Gemini CLI,
Cursor, etc.) working on this repository. Single source of truth;
agent-specific notes live in `CLAUDE.md` and are pointers back here.

## What this project is

A demo / sample under `gke-demos` that ships arbitrary Go source code
into a [Kubernetes Agent Sandbox][as] from a host program, compiles
and executes it, and returns the result. The primary consumer is an
LLM agent calling the MCP server's `run_go_code` tool.

[as]: https://github.com/kubernetes-sigs/agent-sandbox

Three execution layers, from outside in:

1. **MCP server** (`cmd/mcp-server`, stdio transport) — what the agent
   talks to. Holds one sandbox session per process, serializes
   concurrent `Execute` calls, optionally wipes `/app` between calls
   (`--ephemeral`).
2. **Library** (`pkg/goruntime`) — wraps the agent-sandbox Go client.
   `Open` / `Execute` / `Reset` / `Close`. Multi-file uploads via tar
   over the single-file `Write` (the client rejects names with `/`,
   so we tar-stream and `tar -xf` server-side).
3. **In-pod server** (`server/`) — implements the agent-sandbox wire
   contract (`/execute`, `/upload`, `/download`, `/list`, `/exists`)
   over `/app`. Runs commands via `sh -c`. Logs `exec begin` /
   `exec done` per call.

The `cmd/demo` CLI is a thin reference consumer of `pkg/goruntime`;
`cmd/mcp-smoke-test` is a programmatic MCP client that drives
`cmd/mcp-server` end-to-end without an LLM.

## Project conventions

- **Module path:** `github.com/gke-demos/go-runtime-sandbox`
- **License:** Apache 2.0. Every source file carries the full Google
  LLC header at the top — see `CONTRIBUTING.md` for the canonical
  text. `golangci-lint` will eventually enforce this; until then,
  always include the header on new files.
- **Conventional Commits** for commit subject lines (`feat:`, `fix:`,
  `docs:`, `chore:`, etc., with optional scope).
- **DCO sign-off** required on every commit (`git commit -s`). Use
  the human contributor's `user.name` / `user.email`; never sign off
  as the agent. No agent attribution (`Co-Authored-By:`, `Generated
  with …` footers, etc.) in commit messages or PR bodies — these
  projects publish under `gke-demos` and authorship stays clean.
- **Branch protection** on `main`: PR with passing CI (`test`, `lint`,
  `tidy`) is the only way to merge.

## Test layers

- **Unit** (`pkg/format`, `pkg/goruntime`): `go test ./...`. No
  Kubernetes. Runs in seconds. Add tests here for any new logic that
  doesn't depend on a live sandbox.
- **Local kind e2e** (`scripts/run-test-kind.sh`): brings up a kind
  cluster, installs the agent-sandbox controller, deploys our image,
  runs `cmd/demo --flow=all`. Slow (~5 min first run, ~30 s
  incremental). Not in CI by default — Docker-in-Docker needed.
- **MCP smoke** (`cmd/mcp-smoke-test`): spawns `cmd/mcp-server` as a
  subprocess and exercises four scenarios against the live cluster
  context. Run after any change to either the server or the library.

## Things to know before editing

### `_examples/` directory naming

The leading underscore is **intentional** — don't rename it to
`examples/`. Reasons:

- `//go:embed` refuses to traverse into a directory that contains a
  `go.mod`. The `_examples/multifile/` sample needs to look like a
  real module to the agent that builds it inside the sandbox, but a
  nested `go.mod` here would break the embed.
- Go's `./...` package discovery skips directories starting with `_`
  (or `.`). So `_examples/` files don't get compiled as part of this
  module's build — which is what we want, since they're meant to be
  shipped to and built inside the sandbox.

The workaround we chose: drop the nested `go.mod`s, embed everything
under `_examples/`, and have `samples.go` synthesize the `go.mod`
content at load time from a small `goModFor` map. See `samples.go`
and `CLAUDE.md`'s "common edits" if you're adding a new sample.

### Multi-file uploads use tar, not per-file Write

The agent-sandbox Go client's `Write(ctx, name, []byte)` rejects
names containing `/`. We can't change that; it's enforced
client-side. So `pkg/goruntime/execute.go` packs `Request.Files` into
an in-memory tar (`archive/tar`), uploads as a single `Write`, then
`tar -xf` extracts inside the sandbox. The routing decision is
automatic: if any key in `Files` contains `/`, we tar; otherwise
direct `Write`s.

### Cold-start latency varies wildly by cluster mode

- **kind**: 5–10 s for the first `run_go_code` call.
- **GKE Standard** with gVisor node pool capacity: 5–10 s.
- **GKE Autopilot first claim**: up to ~50 s (autoprovision + image
  pull + first build).

Apply `manifests/warmpool.yaml` to eliminate the cold-start cost
when an agent is iterating. Subsequent calls in the same sandbox are
1–2 s either way.

### GKE addon ships an older CRD than upstream

The GKE-bundled `SandboxTemplate` CRD only accepts three top-level
spec fields: `networkPolicy`, `networkPolicyManagement`,
`podTemplate`. The `spec.service: true` toggle in our `manifests/base`
(needed for upstream-v0.4.6-based installs like kind) is rejected
with `unknown field "spec.service"`. The `manifests/overlays/gke-gvisor`
overlay strips it with a `remove` JSON-patch op — that's why GKE
deployments **must** use that overlay (or one derived from it), not
the base directly.

## Common edits and how to verify

| Change                                  | Verify                                                            |
| --------------------------------------- | ----------------------------------------------------------------- |
| Add a new flag to `cmd/mcp-server`      | Update the table in `cmd/mcp-server/README.md` and `docs/mcp-howto.md` |
| Change `pkg/goruntime` API              | Update `cmd/demo` and `cmd/mcp-server` call sites; run `go test ./...` |
| New sample module under `_examples/`    | Add entry to `goModFor` in `samples.go`; embed picks it up via `all:_examples` |
| Edit the runtime image                  | `make image && make e2e-kind` to catch breakage end-to-end        |
| Touch docs under `docs/site/`           | `make docs-serve` for local preview at `http://localhost:1313/go-runtime-sandbox/` |

## Things to avoid

- **Don't add cgo dependencies** to the runtime image — `gcc` is
  intentionally omitted to keep the image small (~502 MB). If an
  agent really needs cgo, that's a follow-up issue, not an in-PR
  change.
- **Don't bypass the warm pool with manual SandboxClaim creation** in
  scripts intended for repeated use — claims from the pool complete
  in 1–2 s; cold creates can take 50 s on Autopilot.
- **Don't commit `.mcp.json` or `.gemini/settings.json`** — they hold
  per-machine absolute paths. The `.example` templates in
  `cmd/mcp-server/` are what's committed.
- **Don't commit the kind-default `mcp-poc`-style image tags** or
  the personal namespace name `go-runtime-sandbox-mcp-poc` — those
  are local-dev shorthand from the development cycle. Public docs
  use `:latest` and `go-runtime-demo` or `default`.
