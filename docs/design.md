# go-runtime-sandbox — Design

Derived from [`spec.md`](./spec.md). The spec answers *what*; this document
answers *how*.

## 1. Overview

Two artifacts, one HTTP contract, one end-to-end test:

```
┌──────────────────────────────┐  port-forward  ┌────────────────────────────┐
│ host                         │ ──────────────▶│ sandbox pod                │
│  cmd/demo (Go)               │   HTTP :8888   │  server (Go, net/http)     │
│  └─ sigs.k8s.io/agent-sandbox│                │  └─ shells out: go, tar    │
│     /clients/go/sandbox      │                │  workdir: /app             │
└──────────────────────────────┘                └────────────────────────────┘
```

The pod's container image (`go-runtime-sandbox`) bundles the Go toolchain
plus our server binary. The server speaks the same HTTP contract as
upstream's Python runtime so the agent-sandbox Go client's `Write` / `Run`
/ `Read` / `List` / `Exists` methods work without modification.

## 2. HTTP contract

The server MUST implement the same wire contract as
`examples/python-runtime-sandbox/main.py`, because the agent-sandbox Go
client is the consumer and assumes that shape. All paths under `/app`.

| Method | Path                                | Request                              | Response (200)                                                          |
| ------ | ----------------------------------- | ------------------------------------ | ----------------------------------------------------------------------- |
| GET    | `/`                                 | —                                    | `{"status":"ok"}` — readiness                                           |
| POST   | `/execute`                          | JSON `{"command":"<shell>"}`         | `{"stdout":"...","stderr":"...","exit_code":N}`                         |
| POST   | `/upload`                           | `multipart/form-data` with `file=@…` | `{"filename":"...","size":N}` (exact shape: match Python)               |
| GET    | `/download/{urlencoded-path:path}`  | —                                    | `application/octet-stream` body of the file                             |
| GET    | `/list/{urlencoded-path:path}`      | —                                    | `[{"name":"x","size":N,"type":"file"\|"directory","mod_time":"..."}]`   |
| GET    | `/exists/{urlencoded-path:path}`    | —                                    | `{"path":"<decoded>","exists":true\|false}`                             |

### Path handling

- Encoded path segment is URL-decoded.
- A leading `/` is stripped.
- The result is joined onto `/app` and resolved (`filepath.EvalSymlinks`
  / `filepath.Abs`).
- If the resolved path is not under `/app`, return `403` with body
  `{"detail":"Access denied: Path must be within /app"}`. This mirrors
  the Python `get_safe_path` behavior so traversal tests pass.

### `/execute` semantics

- Command is **not** interpreted by `/bin/sh` blindly. Python uses
  `shlex.split` + `subprocess.run(... shell=False)`. Go equivalent: parse
  the command with [`mvdan/sh`][mvdansh]-style splitting or a small
  hand-rolled `shlex` (no shell metachars supported), then
  `exec.CommandContext(ctx, argv[0], argv[1:]...)`.
- However, the demo flow needs shell features (`tar -xf x.tar && rm x.tar`,
  `./app`). Two viable approaches:
  1. **Match Python literally** (shlex split, no shell). Then the demo
     issues one `Run` per command and avoids `&&`/redirection.
  2. **Allow shell.** Run the command via `sh -c "<command>"`. Diverges
     from Python but is more ergonomic and matches what an
     "agent-controlled sandbox" actually needs (the security boundary is
     the sandbox itself, not parsing).

  **Decision: (2).** Execute via `sh -c`. Add a one-line note in the
  server README that this differs from upstream Python. Rationale: the
  whole point of the agent-sandbox is to safely run agent-issued
  commands; restricting to `shlex.split` adds friction without security
  benefit when the sandbox is the trust boundary. The runtime image
  therefore needs `/bin/sh` (it does — bookworm-slim ships dash).

- `cwd` is `/app`.
- Stdout/stderr are captured into separate buffers, returned as strings.
  Cap each at 8 MiB to stay well under the client's 16 MiB response cap;
  on overflow, truncate from the *tail* and append `\n... [truncated]`.
  This is a deliberately dumb wire-level backstop — LLM-friendly
  truncation (keep head + tail, elide the middle so trailing error
  messages and panics survive) is the library layer's job (§5a). The
  server stays unaware of who's consuming its output.
- No execution timeout server-side; rely on the client's `WithTimeout`.

### `/upload` semantics

- Multipart form field name: `file` (Python uses FastAPI's `UploadFile =
  File(...)` which defaults to the form field name `file`; the Go client
  must already be sending this — verify by reading the client source
  during implementation).
- Destination: `/app/<filename>` where `<filename>` is the form-provided
  filename, taken as-is (matches Python, which does **not** sanitize
  upload paths — this is fine for our threat model since the sandbox is
  the boundary).
- No subdirectory support in the upload itself — multi-file payloads
  arrive as a tar (see §6).

[mvdansh]: https://github.com/mvdan/sh

## 3. Server (Go)

### Layout

```
server/
├── main.go        # flag parsing, http.Server lifecycle, signal handling
├── handlers.go    # one handler func per endpoint in §2
├── safepath.go    # the /app-rooted path resolver
├── shell.go       # the exec wrapper (sh -c, captured output, truncation)
└── *_test.go      # unit tests per file
```

### Dependencies

Stdlib only. Specifically: `net/http`, `os/exec`, `path/filepath`,
`encoding/json`, `mime/multipart`. No router framework needed — six
routes, all handled with `http.ServeMux` + small dispatchers for the
trailing path-segment routes (`/download/`, `/list/`, `/exists/`).

### Configuration

Flags (with env-var fallbacks):

| Flag           | Env                  | Default | Purpose                          |
| -------------- | -------------------- | ------- | -------------------------------- |
| `--addr`       | `SANDBOX_ADDR`       | `:8888` | Listen address                   |
| `--workdir`    | `SANDBOX_WORKDIR`    | `/app`  | Sandbox root                     |
| `--log-level`  | `SANDBOX_LOG_LEVEL`  | `info`  | `debug`/`info`/`warn`/`error`    |

`/app` is created at startup if absent. The process refuses to start if
it can't write there.

### Lifecycle

- `http.Server` with `ReadHeaderTimeout: 10s`, no body timeout (uploads
  can be large).
- `SIGTERM` / `SIGINT` → `srv.Shutdown(ctx)` with 5 s grace.
- Structured logs to stderr (`log/slog`); request log line per call
  with method, path, status, duration.

### Tests

- Unit tests per handler using `httptest.NewServer`.
- One conformance test that asserts: upload a file via multipart, list
  the workdir, see the file; download it, bytes match; execute
  `echo hi`, get `stdout="hi\n"` and `exit_code=0`; execute a non-zero
  command, get its `exit_code`; traverse attempt (`../etc/passwd`) →
  403.
- Out of scope for PoC: a contract test that wire-shapes match Python
  byte-for-byte. Worth a follow-up.

## 4. Container image

Three-stage Dockerfile:

```dockerfile
# ── Stage 1: source of /usr/local/go (toolchain we ship into the runtime)
FROM golang:1.26-bookworm AS toolchain

# ── Stage 2: build our server binary
FROM golang:1.26-bookworm AS server-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY server/ ./server/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/sandbox-server ./server

# ── Stage 3: slim runtime
FROM debian:bookworm-slim AS runtime
RUN apt-get update \
 && apt-get install -y --no-install-recommends tar ca-certificates git \
 && rm -rf /var/lib/apt/lists/*
COPY --from=toolchain /usr/local/go /usr/local/go
COPY --from=server-build /out/sandbox-server /usr/local/bin/sandbox-server
RUN useradd -m -u 1000 -s /bin/bash sandbox \
 && mkdir -p /app /home/sandbox/.cache/go-build /home/sandbox/go/pkg/mod \
 && chown -R 1000:1000 /app /home/sandbox
USER 1000
WORKDIR /app
ENV PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin \
    GOCACHE=/home/sandbox/.cache/go-build \
    GOMODCACHE=/home/sandbox/go/pkg/mod \
    GOTOOLCHAIN=local
EXPOSE 8888
CMD ["sandbox-server"]
```

### Notes

- **`GOTOOLCHAIN=local`** prevents `go` from downloading a different
  toolchain version when a module's `go` directive is newer than the
  shipped 1.26. Without this, a `go.mod` saying `go 1.27` would trigger a
  multi-hundred-MB download on the first `go build`.
- **`git`** is installed because some `go build` operations require it
  even when the proxy is reachable (replace directives, vcs metadata).
- **No `gcc`/`cc`.** Cgo modules will fail to build. Documented as a
  known limitation; adding `build-essential` doubles the image size.
- **Image size budget:** target < 600 MiB compressed. The `golang:1.26`
  `/usr/local/go` tree is ~450 MiB uncompressed; bookworm-slim is ~30
  MiB; our server binary is < 10 MiB. Headroom for `git` (~50 MiB) and
  `tar` (already present in slim).
- The shipped Go toolchain at `/usr/local/go` is the same one used to
  build our server — pulled from the same `golang:1.26-bookworm` base.
  This avoids skew.

## 5. Demo client (`cmd/demo`)

The CLI demo is a thin wrapper around the `pkg/goruntime` library (§5a).
The split is deliberate: the same library will back a future agent-tool
wrapper (MCP server, Anthropic SDK tool, etc. — see §11), so no
demo-specific logic should leak into the call sites the agent will use.

### Flow

```
1. rt, _ := goruntime.Open(ctx, goruntime.Options{
       Namespace: "default", Template: "go-runtime-template",
   })
   defer rt.Close(ctx)
2. ── Smoke flow (single file) ────────────────────────────────────────
   res, _ := rt.Execute(ctx, goruntime.Request{
       Files:   map[string][]byte{"go.mod": smokeGoMod, "main.go": smokeMainGo},
       Command: "go run main.go",
   })
   printResult("go run", res)
   res, _  = rt.Execute(ctx, goruntime.Request{Command: "go build -o app main.go"})
   printResult("go build", res)
   res, _  = rt.Execute(ctx, goruntime.Request{Command: "./app"})
   printResult("./app", res)          // demonstrates artifact persistence
3. ── Multi-file flow ─────────────────────────────────────────────────
   rt.Execute(ctx, goruntime.Request{Command: "rm -rf -- *"}) // clean slate
   files := flatten(multifileFS)                              // see §5a
   res, _ := rt.Execute(ctx, goruntime.Request{
       Files: files, Command: "go build -o app ./...",
   })
   printResult("multi-file build", res)
   res, _  = rt.Execute(ctx, goruntime.Request{Command: "./app"})
   printResult("multi-file run", res)
   res, _  = rt.Execute(ctx, goruntime.Request{Command: "go test ./..."})
   printResult("multi-file test", res) // optional
4. log "PoC complete"; deferred Close() tears down the sandbox claim.
```

Note that successive `Execute` calls reuse the same sandbox — `/app`,
the module cache, and the build cache all persist. The library does
not create-and-destroy per call. The smoke flow's `./app` step proves
this directly: the binary built in the previous call is still there
in the next. This is exactly the property an agent tool needs (one
sandbox per conversation, many tool calls against it).

### Embedded samples

```go
//go:embed examples/smoke/main.go
var smokeMainGo []byte

//go:embed examples/smoke/go.mod
var smokeGoMod []byte

//go:embed examples/multifile/*
//go:embed examples/multifile/greet/*
var multifileFS embed.FS
```

`examples/smoke/` and `examples/multifile/` are real, compilable Go
modules in the repo (each with its own `go.mod`, so they're sub-modules
of the parent workspace and won't interfere with the top-level build).
That lets a developer `cd examples/multifile && go run .` locally as a
sanity check before involving the sandbox.

### CLI

```
demo [--namespace=default] [--template=go-runtime-template] [--flow=all|smoke|multi]
     [--claim=NAME]   # if set, reattach to an existing sandbox instead of creating one
     [--keep]         # on exit, Disconnect instead of Close (sandbox survives)
```

Exits non-zero on any step failure, with the failing stage named in the
error. `--claim` + `--keep` together let a developer iterate against a
warm sandbox: first run creates and keeps; subsequent runs reattach.

## 5a. Library: `pkg/goruntime`

Both the CLI demo and any future agent-tool wrapper consume this
package. It knows nothing about Cobra, MCP, the Anthropic SDK, or
LLMs — it's a plain Go API around the
"materialize files, run a command, get a result" workflow on top of
the agent-sandbox Go client.

### API surface

```go
package goruntime

// Options configures how a Session attaches to (or creates) a sandbox.
type Options struct {
    Namespace string          // k8s namespace
    Template  string          // SandboxTemplate name
    ClaimName string          // "" = create new; non-empty = reattach
    Client    *sandbox.Client // optional; built from Namespace if nil
    Truncate  TruncateConfig  // result-truncation policy; zero = defaults
}

// TruncateConfig controls LLM-friendly head+tail truncation of Result
// stdout/stderr. Set both to 0 to disable library-level truncation
// (the wire-level 8 MiB cap from §2 still applies).
type TruncateConfig struct {
    HeadBytes int // bytes to keep from the start (default: 8192)
    TailBytes int // bytes to keep from the end   (default: 8192)
}

// Request is a single execution: drop files, run a shell command in /app.
type Request struct {
    Files   map[string][]byte // dest path (may contain "/") -> contents
    Command string            // shell command, run via sh -c in /app
    Timeout time.Duration     // 0 = library default (5 min)
}

// Result captures what the command produced, post-truncation.
type Result struct {
    Stdout          string
    Stderr          string
    ExitCode        int
    Duration        time.Duration
    StdoutTruncated bool
    StderrTruncated bool
}

func Open(ctx context.Context, opts Options) (*Session, error)

func (s *Session) Execute(ctx context.Context, req Request) (*Result, error)
func (s *Session) ClaimName() string                       // for the caller to persist
func (s *Session) Disconnect(ctx context.Context) error    // keeps sandbox alive
func (s *Session) Close(ctx context.Context) error         // tears sandbox down
```

### Behavior

- `Open` calls `client.CreateSandbox` when `ClaimName == ""` and
  `client.GetSandbox` otherwise. Reattach works even after a prior
  process exited without `Close` — the sandbox is a Kubernetes
  resource, not in-process state. This is the property that makes the
  agent-tool flow work: each tool call is a fresh OS process, but the
  sandbox lives across calls keyed by `ClaimName`.
- `Execute` materializes `req.Files` under `/app` before running
  `req.Command`. Routing is automatic: zero or one file, or all keys
  at the workdir root → direct `Write` calls; any key containing `/`
  → tar via §6. Files not in `req.Files` are left alone; `/app` is
  persistent across `Execute` calls. To reset, the caller passes
  `{Command: "rm -rf -- *"}` (or a `Files` map of empty contents for
  the specific files it wants to overwrite).
- Truncation: the server may return up to 8 MiB per stream (its dumb
  backstop). The library then applies head+tail truncation per
  `opts.Truncate`. If `len(stdout) > Head + Tail`, the result is
  `stdout[:Head] + "\n... [N bytes elided] ...\n" + stdout[len-Tail:]`
  with `StdoutTruncated = true`. Default 8 KiB + 8 KiB = 16 KiB cap —
  comfortably within an LLM context window with room for the agent's
  reasoning around it.
- `Disconnect` vs `Close`: callers that want the sandbox to outlive
  the current process call `Disconnect` (and persist the
  `ClaimName`); the CLI demo's default `defer rt.Close(ctx)` tears
  it down entirely.

### Why this shape

The schema for a future agent tool falls out almost directly:

```json
{
  "name": "run_go_code",
  "description": "Execute Go code in an isolated sandbox. Files are written under /app and the command is run there. State persists across calls in the same session.",
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

The tool implementation is then ~30 lines: decode args → look up the
caller's `ClaimName` (per-conversation) → `goruntime.Open` →
`goruntime.Execute` → format `Result` into a string the model can
read. Session management (one sandbox per agent conversation) lives
entirely in the wrapping layer, not in `goruntime`. None of that
belongs in this PoC — what belongs is making sure the library makes
that wrapping a half-day's work, not a refactor.

## 6. Multi-file upload: tar over a single `Write`

The agent-sandbox Go client's `Write(ctx, name, bytes)` accepts only a
plain filename — names containing `/` are rejected client-side. To ship
a directory tree we therefore:

1. In the demo, walk the embedded `examples/multifile` FS and build an
   in-memory tar archive (`archive/tar`, uncompressed — it's small,
   compression buys little and adds dependencies).
2. `sb.Write(ctx, "module.tar", tarBytes)` — uploads as a single blob to
   `/app/module.tar`.
3. `sb.Run(ctx, "tar -xf module.tar && rm module.tar")` — unpacks
   under `/app` and removes the archive.
4. Subsequent `Run` calls operate on the materialized tree.

### Why not the alternatives

- **Per-file uploads with subpaths.** Would require extending the
  server's `/upload` with a destination-path form field *and* a
  matching client API change. Neither is in our control: changing the
  upstream client is out of scope, and the existing `Write` rejects
  `/` before the request ever leaves the host.
- **Per-file uploads with flat names + server-side `mv`.** Works for
  files-at-root but can't reconstruct subdirectories without invented
  naming conventions (e.g., `__greet__greet.go`), which is ugly and
  collision-prone.
- **`scp`/`rsync` over a side channel.** Overkill; no side channel
  exists.

### Server impact

None. `tar` is already in the runtime image (added for this purpose,
~free in `bookworm-slim`). The mechanism is pure client behavior over
the existing contract.

### Limits

`Write` caps at 256 MiB by default — well above what any reasonable
agent-generated module would tar to. If we ever needed more, the
mechanism would have to chunk; not a PoC concern.

## 7. `SandboxTemplate` manifest

```yaml
# manifests/sandbox-template.yaml
apiVersion: agents.x-k8s.io/v1alpha1
kind: SandboxTemplate
metadata:
  name: go-runtime-template
  namespace: default
spec:
  podTemplate:
    metadata:
      labels:
        sandbox: go-runtime-sandbox
    spec:
      containers:
        - name: go-runtime
          image: go-runtime-sandbox:latest
          imagePullPolicy: IfNotPresent
          ports:
            - containerPort: 8888
          readinessProbe:
            httpGet: { path: /, port: 8888 }
            periodSeconds: 2
          resources:
            requests: { cpu: "100m", memory: "256Mi" }
            limits:   { cpu: "2",    memory: "2Gi"   }
```

`2Gi` memory limit because `go build` of a non-trivial module
plus the module cache comfortably exceeds defaults. Tuned during
testing.

## 8. Kind end-to-end (`scripts/run-test-kind.sh`)

Mirrors `examples/python-runtime-sandbox/run-test-kind.sh`:

```
set -euo pipefail
KIND_CLUSTER_NAME="agent-sandbox"

# 1. Bring up / reuse kind cluster, install agent-sandbox controller
(cd "$AGENT_SANDBOX_REPO" && make build && make deploy-kind)

# 2. Build & load our image
docker build -t go-runtime-sandbox:latest .
kind load docker-image go-runtime-sandbox:latest --name "$KIND_CLUSTER_NAME"

# 3. Apply the template
kubectl apply -f manifests/sandbox-template.yaml

# 4. Cleanup trap
cleanup() {
  kubectl delete -f manifests/sandbox-template.yaml --ignore-not-found
  # (controller + cluster teardown left to the operator to avoid churn
  #  on iterative dev; document the manual `kind delete cluster` step)
}
trap cleanup EXIT

# 5. Run the demo — it does its own port-forward via the Go client
go run ./cmd/demo --flow=all
```

`AGENT_SANDBOX_REPO` is an env var the operator sets to the path of a
checkout of `kubernetes-sigs/agent-sandbox` (needed because the
controller install isn't a `helm install one-liner` yet). The script
fails fast with a helpful message if it's unset or invalid.

## 9. Repository layout

```
go-runtime-sandbox/
├── docs/
│   ├── spec.md
│   └── design.md
├── go.mod                        # module: github.com/gke-demos/go-runtime-sandbox
├── go.sum
├── Dockerfile
├── server/
│   ├── main.go
│   ├── handlers.go
│   ├── safepath.go
│   ├── shell.go
│   └── *_test.go
├── pkg/goruntime/                # library — backs CLI demo AND future agent tool
│   ├── session.go                # Open, Session, Close, Disconnect, ClaimName
│   ├── execute.go                # Execute, file materialization (Write vs tar)
│   ├── truncate.go               # head+tail truncation helper
│   └── *_test.go
├── cmd/demo/
│   └── main.go                   # thin CLI wrapper over pkg/goruntime
├── examples/
│   ├── smoke/                    # own go.mod — sub-module
│   │   ├── go.mod
│   │   └── main.go
│   └── multifile/                # own go.mod — sub-module
│       ├── go.mod
│       ├── main.go
│       └── greet/
│           └── greet.go
├── manifests/
│   └── sandbox-template.yaml
└── scripts/
    └── run-test-kind.sh
```

### Module organization

- Top-level `go.mod` contains the **server** and the **demo client**.
  These share no code but live in one module for build simplicity.
- `examples/smoke/` and `examples/multifile/` each have their own
  `go.mod` — Go treats nested `go.mod` directories as separate modules,
  so they're transparently excluded from the parent's build graph. This
  is what we want: the parent compiles the server and demo without
  pulling the sample modules' contents into its own build.
- The demo `//go:embed`s the sample files as raw bytes / `embed.FS`,
  so it doesn't import them as Go code — the nested `go.mod` is
  irrelevant to the demo's compilation.

## 10. Implementation order

Recommended sequence so each step is verifiable in isolation:

1. **Server, locally.** Implement §3, run with `go run ./server`,
   exercise endpoints with `curl`. (No Kubernetes involved.)
2. **Dockerfile.** Build, run `docker run -p 8888:8888
   go-runtime-sandbox:latest`, repeat the `curl` tests.
3. **`pkg/goruntime` — single-file path.** Implement `Open`, `Close`,
   `Disconnect`, `ClaimName`, `Execute` with only the direct `Write`
   path (no tar yet). Unit-test against the local container using the
   agent-sandbox Go client's `APIURL` option pointed at
   `http://127.0.0.1:8888`. Confirms the contract end-to-end.
4. **`pkg/goruntime` — multi-file path + truncation.** Add the tar
   branch from §6 and `truncate.go` per §5a. Unit-test multi-file
   materialization and verify head+tail truncation of long output.
5. **CLI demo.** Implement `cmd/demo` as the thin wrapper from §5,
   driving both flows. Still against the local container.
6. **Kind path.** Add the manifest (§7) and script (§8). Switch the
   demo's connectivity to port-forward (its default — just drop
   `APIURL`).
7. **Polish:** logging, error messages, README.

## 11. Open follow-ups (post-PoC)

- **Agent-tool wrapper.** A small MCP server (or an Anthropic SDK
  tool definition consumed directly by a Go agent) that exposes
  `goruntime.Execute` as a single `run_go_code` tool with the input
  schema sketched in §5a. The wrapper holds a `map[conversationID]
  claimName` so each conversation gets a persistent sandbox across
  tool calls; on tool invocation it calls `goruntime.Open` with the
  remembered claim (or creates one and remembers it), calls
  `Execute`, then `Disconnect`. Estimate: half a day, given the
  library shape in §5a.
- Contract conformance test that runs the same battery against the
  Python and Go servers and diffs the responses.
- Cgo support (`gcc` + `libc-dev` in the runtime).
- Build-cache persistence across sandbox lifecycles via a PVC
  (matters more once an agent is iterating many times against the
  same conversation's sandbox).
- Gateway-mode connectivity, TLS to the sandbox.
- A `tar` helper inside the server (`POST /upload-tar?extract=true`) so
  multi-file becomes a single round-trip instead of `Write` + `Run`.
  Only worth doing if the two-step latency becomes a problem.
- Sandbox warm pool (`SandboxWarmPool` CRD) so agent conversations
  don't pay cold-start cost on first tool call.
