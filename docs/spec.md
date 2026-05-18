# go-runtime-sandbox — Specification

## 1. Goal

Demonstrate, end-to-end, that arbitrary Go source code can be written into a
GKE / Kubernetes [Agent Sandbox][as] from a host program and then compiled and
executed inside that sandbox, with stdout / stderr / exit-code returned to the
host. This is the Go analogue of the upstream [`python-runtime-sandbox`][py]
example.

[as]: https://github.com/kubernetes-sigs/agent-sandbox
[py]: https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/python-runtime-sandbox

## 2. Background

The agent-sandbox Go client (`sigs.k8s.io/agent-sandbox/clients/go/sandbox`)
talks to a sandbox pod over HTTP on a single backend port (default `8888`),
brokered by `sandbox-router-svc` and either a Gateway or a port-forward. The
high-level `Handle` methods map 1:1 onto HTTP endpoints implemented inside the
pod's container:

| Client call           | HTTP endpoint                       |
| --------------------- | ----------------------------------- |
| `Run(ctx, cmd)`       | `POST /execute`                     |
| `Write(ctx, n, b)`    | `POST /upload`                      |
| `Read(ctx, n)`        | `GET  /download/{urlencoded-path}`  |
| `List(ctx, dir)`      | `GET  /list/{urlencoded-path}`      |
| `Exists(ctx, n)`      | `GET  /exists/{urlencoded-path}`    |
| (readiness)           | `GET  /`                            |

A "runtime sandbox" image is therefore just: a container that listens on
`8888`, implements the above contract over a sandboxed working directory
(`/app`), and ships whatever language toolchain the workload needs. The
Python image bundles `python3` + FastAPI; ours bundles the Go toolchain.

## 3. Deliverables

Two artifacts, plus glue:

1. **`go-runtime-sandbox` container image.** A Linux image that
   - exposes port `8888`,
   - implements the HTTP contract in §2 with `/app` as the sandboxed root,
   - has a working Go toolchain (`go build`, `go run`, `go mod`) on `PATH`,
   - runs as a non-root user (UID 1000, matching the Python example),
   - is small enough to load into `kind` in a few seconds.

2. **`cmd/demo` Go client program.** A command-line program that uses the
   agent-sandbox Go client to demonstrate two flows against the same
   sandbox:

   1. **Single-file smoke flow.**
      a. Create a `Sandbox` from a `SandboxTemplate` pointing at the image
         above.
      b. `Write` a single `main.go` (stdlib-only, prints a message and does
         something non-trivial) into the sandbox.
      c. `Run "go run main.go"` and print stdout / stderr / exit code.
      d. `Run "go build -o app main.go"` followed by `Run "./app"` to prove
         the build artifact is a working binary that can be re-executed.

   2. **Multi-file module flow.** (See §3a for how source is shipped.)
      a. `Write` each file of an embedded sample module (a `go.mod` plus
         two or more `.go` files across at least one sub-package) into the
         sandbox.
      b. `Run "go build -o app ./..."` then `Run "./app"`, printing output.
      c. Optionally `Run "go test ./..."` to show the module's tests pass
         inside the sandbox.

   3. Tear the sandbox down via `EnableAutoCleanup` + `DeleteAll` on exit.

3. **Kind-based integration test** (`scripts/run-test-kind.sh`) mirroring the
   Python example's flow: build the image, load it into kind, apply a
   `SandboxTemplate` manifest, run `cmd/demo` (which exercises both flows
   above), clean up on exit. The smoke flow must pass first; the multi-file
   flow gates "PoC complete."

### 3a. Shipping multi-file source

The agent-sandbox Go client's `Write(ctx, name, bytes)` only handles one
file at a time and rejects names containing `/`. To ship a module:

- The demo embeds the sample module's tree via `//go:embed` so it travels
  with the binary.
- For each embedded file, the client first calls
  `Run("mkdir -p <subdir>")` if needed, then `Write`s the file. (Per the
  client docs, `Write` requires a plain filename, but the server-side
  upload handler can place it at an arbitrary path under `/app` — the
  exact mechanism is a design-doc concern; one workable approach is for
  the server to accept a destination-path form field, and for the demo to
  call `Write` against the working directory after `cd`-ing via `Run`.)
- Investigating whether `tar`-streaming a directory via a single `Write`
  + a server-side untar `Run` is a cleaner alternative is left to the
  design doc.

## 4. Design decisions

The following are settled and bind the design doc:

- **Server language: Go.** The HTTP server in §2's contract is implemented
  in Go (`net/http`, stdlib only where reasonable). The whole PoC —
  server, demo client, and any tooling — is Go end-to-end.

- **Run modes: both `go run` and `go build`/exec.** The demo exercises
  both so we prove (a) ad-hoc execution works and (b) a build produces a
  reusable binary inside the sandbox that can be invoked separately.

- **Image build: multi-stage with a slim runtime copying in
  `/usr/local/go`.** Stage 1 is `FROM golang:1.26 AS goroot` (or similar)
  used only as a source of the toolchain. Stage 2 is `FROM golang:1.26 AS
  server-build` that compiles the server binary. Stage 3 is a slim
  runtime (e.g. `debian:bookworm-slim` or `gcr.io/distroless/base-debian12`
  if it has the tools `go` needs at runtime — `cc` for cgo is a known
  catch; the design doc decides) that:
  - copies `/usr/local/go` from the goroot stage,
  - copies the server binary from the server-build stage,
  - sets `PATH=/usr/local/go/bin:$PATH` and a writable `GOCACHE` /
    `GOMODCACHE` under the non-root user's home,
  - runs as UID 1000, exposes 8888, and `CMD`s the server.

  Target: smallest viable image that can still `go build` a module with
  modest dependencies.

- **Module mode: always operate in a Go module.** Single-file smoke uses
  one `main.go` and the server (or the demo) ensures a minimal `go.mod`
  exists in `/app` so `go run` / `go build` work in module mode without
  GOPATH fallbacks. Multi-file flow uploads a real `go.mod`. The design
  doc decides whether the server auto-creates a default `go.mod` when one
  is absent, or whether the demo always uploads one — leaning toward the
  latter for simplicity and predictability.

- **Connectivity: port-forward.** The demo uses the default port-forward
  mode of the Go client. No Gateway, no custom `APIURL`. Gateway mode is
  a follow-up beyond this PoC.

## 5. Out of scope (for this PoC)

- Vendored dependencies and private module proxies. The multi-file sample
  module either has no third-party deps or pulls them from public
  `proxy.golang.org` at `go build` time.
- Caching the Go build cache across sandbox lifecycles.
- TLS / private CA configuration on the client.
- Production hardening of the server (auth, rate limiting, structured
  logging beyond what the Python example has).
- gVisor / Kata runtime configuration — the sandbox CRD allows it but the
  PoC runs on whatever kind provides.
- Gateway-mode connectivity (port-forward only, per §4).

## 6. Success criteria

On a fresh laptop with Docker + kind + `kubectl` + Go installed, running
`scripts/run-test-kind.sh` exits `0` after producing output that includes:

1. **Smoke (single-file):** stdout of a Go program whose source was
   uploaded from the host at runtime (nothing about that source existed
   on disk at image build time), executed via both `go run` and a built
   binary.
2. **PoC complete (multi-file):** stdout of a multi-file Go module —
   `go.mod` plus source spanning at least one sub-package — that was
   uploaded file-by-file from the host, built via `go build ./...`, and
   executed inside the sandbox.

## 7. Repository layout (proposed)

```
go-runtime-sandbox/
├── docs/
│   ├── spec.md                   # this file
│   └── design.md                 # to be generated from this spec
├── Dockerfile                    # multi-stage build (§4)
├── go.mod                        # workspace module for server + demo
├── server/
│   └── main.go                   # HTTP server implementing §2 contract
├── cmd/demo/
│   └── main.go                   # client program (§3.2)
├── examples/
│   ├── smoke/
│   │   └── main.go               # single-file sample (embedded by demo)
│   └── multifile/                # multi-file sample (embedded by demo)
│       ├── go.mod
│       ├── main.go
│       └── greet/
│           └── greet.go
├── manifests/
│   └── sandbox-template.yaml     # SandboxTemplate pointing at the image
└── scripts/
    └── run-test-kind.sh          # end-to-end kind test (§3.3)
```
