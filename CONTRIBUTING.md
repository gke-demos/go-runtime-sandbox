# Contributing to go-runtime-sandbox

Thanks for your interest in contributing! By participating in this
project you agree to abide by the [Code of Conduct](./CODE_OF_CONDUCT.md).

## Reporting bugs and requesting features

- **Bugs:** [open an issue](https://github.com/gke-demos/go-runtime-sandbox/issues/new)
  and include your Kubernetes version (GKE addon version if applicable),
  the sandbox-runtime image tag, the namespace layout you're using, and
  the failure mode (sandbox pod logs, MCP server stderr, `kubectl
  describe sandbox <name>`).
- **Feature requests:** check the [open issues](https://github.com/gke-demos/go-runtime-sandbox/issues)
  first — your idea may already be tracked. If not, file an issue with
  the use case (what you're trying to do) before the proposed solution.
- **Questions / discussion:**
  [GitHub Discussions](https://github.com/gke-demos/go-runtime-sandbox/discussions).

## Pull requests

### Before you start

For anything beyond a typo fix or one-line bug, open an issue first so
we can agree on the approach. PRs that are aligned upfront merge faster
than ones that surface a design disagreement at review time.

### Workflow

1. Fork and create a short-lived feature branch off `main` (e.g.
   `feat/cgo-support`, `fix/tar-symlink-handling`, `docs/install`).
2. Make your change. Keep the diff focused; unrelated cleanup belongs
   in a separate PR.
3. Run the standard checks locally before pushing:
   ```bash
   make test          # go test + vet
   make lint          # golangci-lint
   ```
4. Open the PR against `main`. CI runs `test`, `lint`, and
   `tidy` (verifying `go mod tidy` is clean) on every PR; all three
   must pass before merge.

### Commit messages — Conventional Commits

Subject lines follow [Conventional Commits](https://www.conventionalcommits.org/):

- `feat:` — user-visible new functionality
- `fix:` — user-visible bug fix
- `docs:` — documentation only
- `test:` — tests only
- `refactor:` — code change that's neither a feature nor a fix
- `chore:` / `build:` / `ci:` — repo plumbing

Optional scope in parens: `feat(mcp-server): add --ephemeral flag`,
`fix(goruntime): respect Files=nil correctly`. Keep the subject under
~70 chars; put detail in the body explaining *why* and what
verification you did.

### Developer Certificate of Origin (DCO)

All commits must be **signed off** under the
[Developer Certificate of Origin](https://developercertificate.org/).
The DCO is a lightweight assertion that you wrote the patch (or have
the right to submit it under the project's Apache-2.0 license) — it's
a `Signed-off-by:` trailer in the commit message, not a cryptographic
signature.

Sign off by passing `-s` to `git commit`:

```bash
git commit -s -m "feat(mcp-server): add --ephemeral flag"
```

…which appends:

```
Signed-off-by: Your Name <you@example.com>
```

The name and email must match your `git config user.name` /
`user.email`. If you forget, amend with `git commit --amend -s` (single
commit) or rebase with `-x 'git commit --amend -s --no-edit'`
(multiple).

### License headers

Every source file carries the full Apache 2.0 header attributed to
Google LLC. For Go files:

```
/*
Copyright 2026 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
```

For YAML, Dockerfile, shell, Makefile, and other hash-comment files,
use `#` line prefixes instead of `/* */`.

### Tests

The repo has two test layers:

- **Unit** (`pkg/format`, `pkg/goruntime`) — pure logic, no
  Kubernetes. `go test ./...` runs these in seconds.
- **End-to-end** (`scripts/run-test-kind.sh`,
  `cmd/mcp-smoke-test`) — exercise the full stack against a kind
  cluster with the agent-sandbox controller. Not run in CI by default
  (would need a longer-running runner with Docker-in-Docker); contributors
  run them locally and paste results into the PR for non-trivial
  changes.

A new feature without at least a unit test is incomplete. A bug fix
without a regression test makes it easy for the bug to come back.

## Project layout

- `server/` — HTTP server inside the sandbox pod (agent-sandbox wire
  contract).
- `pkg/goruntime/` — Go library wrapping the agent-sandbox client.
- `pkg/format/` — `Result → string` formatter for LLM consumption.
- `cmd/mcp-server/` — MCP server exposing `run_go_code`.
- `cmd/demo/` — CLI demo wrapper.
- `cmd/mcp-smoke-test/` — programmatic MCP client for end-to-end
  validation.
- `manifests/` — kustomize bases + overlays + warmpool.
- `_examples/` — sample Go modules embedded by `cmd/demo`. The
  underscore prefix is intentional: `//go:embed` can't traverse into
  a directory containing a `go.mod`, and naming it `_examples` keeps
  Go's `./...` from trying to compile it as part of this module.
- `docs/site/` — Hugo source for the published documentation site.
- `.github/workflows/` — CI, image build, docs deploy, release.

## License

By contributing, you agree that your contributions will be licensed
under the [Apache License 2.0](./LICENSE).
