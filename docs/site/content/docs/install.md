---
title: "Install"
linkTitle: "Install"
weight: 10
description: "Run the demo on kind or GKE — prerequisites, manifest overlays, warm pool, troubleshooting."
---


This walks you through running the `cmd/demo` program end-to-end against a
Kubernetes cluster. Two paths are supported:

- **A. Local kind cluster** — recommended for the first run. One script.
- **B. GKE cluster** — for running the demo against real cloud
  infrastructure (and the eventual gVisor-isolated runtime path).

Both paths exercise the same demo: ship a Go source file into a sandbox,
compile it, run it, then do the same for a multi-file module.

---

## Prerequisites

### Common (both paths)

| Tool       | Version   | Why                                                |
| ---------- | --------- | -------------------------------------------------- |
| Docker     | 20.10+    | Build the sandbox runtime image                    |
| Go         | 1.26+     | Build/run `cmd/demo` and the server binary         |
| `kubectl`  | 1.30+     | Apply manifests, talk to the cluster               |
| `curl`     | any       | Optional — used by the kind script and tutorials   |
| `git`      | any       | Docker uses git-context to fetch the router source |

### Path A only (kind)

| Tool   | Version | Why                                  |
| ------ | ------- | ------------------------------------ |
| `kind` | 0.20+   | Spins up a local Kubernetes cluster  |

### Path B only (GKE)

| Tool                 | Why                                                |
| -------------------- | -------------------------------------------------- |
| `gcloud` CLI         | Create the cluster, push images, get credentials   |
| A GCP project        | Anywhere you can enable GKE + Artifact Registry    |
| An Artifact Registry | A Docker-format repo to push two images to         |

---

## Path A — local kind cluster (recommended first run)

From the repo root:

```bash
./scripts/run-test-kind.sh
```

That script:

1. Creates a kind cluster named `agent-sandbox-poc` (reused on subsequent
   runs).
2. Installs the agent-sandbox controller via the upstream v0.4.6 release
   manifests.
3. Builds the `sandbox-router` image (Docker pulls the router source
   straight from the upstream repo at the matching version).
4. Builds this repo's `go-runtime-sandbox` image and loads both images
   into the kind cluster.
5. Applies `manifests/base` (creates the `go-runtime-template`
   `SandboxTemplate` with no `runtimeClassName` — kind has no
   sandbox runtime). See [Picking a runtime class](#picking-a-runtime-class)
   below for the gVisor overlay.
6. Runs `go run ./cmd/demo --flow=all` against the cluster.

Expected tail of output:

```
=== single-file smoke flow ===
-- go run main.go [exit=0] --                hello from the sandbox! sum(1..100)=5050, go=go1.26.3/linux/amd64
-- ./app (re-runs the binary ...) [exit=0] -- hello from the sandbox! ...

=== multi-file module flow ===
-- ./app [exit=0] --                         hello, Alice — from the multi-file sandbox sample
                                             hello, Bob — ...
                                             hello, Carol — ...
-- go test ./... [exit=0] --                 ok  example.com/multifile/greet  0.004s

==> PoC complete
```

To tear the kind cluster down entirely:

```bash
kind delete cluster --name agent-sandbox-poc
```

The script's own cleanup only deletes per-test resources (template,
router), so iterating is fast.

---

## Path B — GKE cluster

On GKE the agent-sandbox controller is a **built-in addon** (Preview)
that you turn on with a single `gcloud` flag — no manifest install,
no separate controller deployment to manage. The addon installs the
CRDs, the controller, and a Validating Admission Policy that enforces
gVisor + non-root + dropped capabilities + resource limits on sandbox
pods. Our image and the router are application-layer components and
still need to be built and deployed.

### 1. Enable the agent-sandbox addon

Follow the official guide and come back here once `kubectl get crd
sandboxtemplates.extensions.agents.x-k8s.io` returns the CRD:

→ **[Install Agent Sandbox on GKE][gke-as-install]**

That guide covers: minimum GKE version (1.35.2-gke.1269000+),
Autopilot vs. Standard, the gVisor node pool requirement for
Standard, and the `--enable-agent-sandbox` flags for cluster create
and update.

Get credentials and confirm the addon is enabled:

```bash
gcloud container clusters get-credentials YOUR_CLUSTER \
  --region YOUR_REGION --project YOUR_PROJECT

# Confirm the CRDs are present (the most reliable signal that the
# addon is actually installed — the gcloud `describe` flag has been
# observed to return blank rather than False when the addon isn't
# enabled, so trust kubectl here):
kubectl get crd | grep agents.x-k8s.io
# expect: sandboxclaims, sandboxes, sandboxtemplates, sandboxwarmpools
```

[gke-as-install]: https://docs.cloud.google.com/kubernetes-engine/docs/how-to/how-install-agent-sandbox

> **Use the `gke-gvisor` overlay on GKE.** The addon's Validating
> Admission Policy enforces stricter pod requirements than the basic
> `gvisor` overlay provides (non-root, no privilege escalation, dropped
> capabilities, RuntimeDefault seccomp, gVisor node selector +
> toleration). The overlay also strips `spec.service: true` from the
> base — GKE's bundled `SandboxTemplate` CRD is older than upstream
> v0.4.6 and rejects that field (the addon creates the per-sandbox
> Service unconditionally, so the toggle isn't needed). Use the
> overlay in step 5 below; if sandbox pods still fail admission,
> `kubectl describe sandbox <name>` and `kubectl get events` will show
> the VAP message.

### 2. Set up Artifact Registry

```bash
REGION=us-central1                       # or wherever your cluster lives
PROJECT=$(gcloud config get-value project)
REPO=agent-sandbox-demo

gcloud artifacts repositories create "$REPO" \
  --repository-format=docker --location="$REGION" \
  --description="Images for the go-runtime-sandbox demo"

gcloud auth configure-docker "$REGION-docker.pkg.dev"
REG="$REGION-docker.pkg.dev/$PROJECT/$REPO"
```

### 3. Build and push the two images

`sandbox-router` (built from the upstream repo via Docker git-context).
Pin to a recent agent-sandbox tag; the router is a stable reverse
proxy so exact-version match with the GKE addon isn't required:

```bash
AS_VERSION=v0.4.6
docker build -t "$REG/sandbox-router:$AS_VERSION" \
  "https://github.com/kubernetes-sigs/agent-sandbox.git#${AS_VERSION}:clients/python/agentic-sandbox-client/sandbox-router"
docker push "$REG/sandbox-router:$AS_VERSION"
```

`go-runtime-sandbox` (this repo) — **optional**. The default
manifest references the public image at
`ghcr.io/gke-demos/go-runtime-sandbox:latest`, which works out of the
box on any cluster that can reach `ghcr.io`. Only build and push your
own if you've customized the runtime image:

```bash
docker build -t "$REG/go-runtime-sandbox:latest" .
docker push "$REG/go-runtime-sandbox:latest"
```

### 4. Deploy the router in the namespace you'll use

The router lives in the **same namespace** as your sandboxes because
the client looks for `sandbox-router-svc` there. Pick a namespace (we
use `default` below):

```bash
NS=default

curl -sfL "https://raw.githubusercontent.com/kubernetes-sigs/agent-sandbox/${AS_VERSION}/clients/python/agentic-sandbox-client/sandbox-router/sandbox_router.yaml" \
  | sed -e "s|\${ROUTER_IMAGE}|$REG/sandbox-router:$AS_VERSION|g" \
  | kubectl -n "$NS" apply -f -

kubectl -n "$NS" rollout status deployment/sandbox-router-deployment --timeout=180s
```

### 5. Apply the SandboxTemplate

Use the `gke-gvisor` overlay — it adds the security context, node
selector, and toleration that the addon's Validating Admission Policy
requires. The shipped manifest references the public GHCR image, so
this is a single command:

```bash
kubectl kustomize manifests/overlays/gke-gvisor | kubectl -n "$NS" apply -f -
```

If you pushed a custom image in step 3, substitute it:

```bash
kubectl kustomize manifests/overlays/gke-gvisor \
  | sed "s|ghcr.io/gke-demos/go-runtime-sandbox:latest|$REG/go-runtime-sandbox:latest|" \
  | kubectl -n "$NS" apply -f -
```

On GKE Autopilot the nodeSelector/toleration are harmless and won't
block scheduling. See
[Picking a runtime class](#picking-a-runtime-class) below for the full
overlay layout.

### 6. (Optional) Apply a warm pool

By default the first tool call from an agent pays the cold-start cost
of scheduling and starting a sandbox pod (~5–10s on GKE). A
`SandboxWarmPool` keeps N pods pre-warmed so claims complete in under
a second:

```bash
kubectl apply -f manifests/warmpool.yaml
```

Tune `spec.replicas` in that file to your expected concurrent-session
count (it defaults to 2 — fine for a demo). See
[Warm pool](#warm-pool) below.

### 7. Run the demo

```bash
go run ./cmd/demo --namespace="$NS" --template=go-runtime-template --flow=all
```

The demo uses port-forward connectivity by default, so it works from
any machine whose `kubectl` context points at the cluster — no Gateway
required for the PoC.

---

## Iterating on the demo

The default flow creates a sandbox, runs both flows, then deletes the
sandbox. For faster iteration, keep a sandbox alive across runs:

```bash
# first run: create, do the smoke flow, leave it alive
go run ./cmd/demo --flow=smoke --keep
# note the printed claim name, e.g. sandbox-claim-abc12

# subsequent runs: reattach to the same sandbox, build cache stays warm
go run ./cmd/demo --flow=multi --claim=sandbox-claim-abc12 --keep

# when you're done:
go run ./cmd/demo --claim=sandbox-claim-abc12      # no --keep → deletes it
```

This is the same pattern the MCP server uses (see
[mcp-howto.md]({{< relref "mcp.md" >}})) — one sandbox per conversation,
persistent across many tool calls.

---

## Picking a runtime class

The shipped manifests are split so the `SandboxTemplate` does not
include `runtimeClassName` by default — that field's only meaningful
when the cluster has a matching `RuntimeClass`, and clusters without
one will reject pods that reference a missing class.

| Layout                            | Effect                                                                                              | When to use                                                                       |
| --------------------------------- | --------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| `manifests/base`                  | Template with no `runtimeClassName`                                                                 | kind, any cluster without a sandbox runtime, smoke testing                        |
| `manifests/overlays/gvisor`       | Adds `runtimeClassName: gvisor`                                                                     | Any cluster with a gVisor `RuntimeClass` (not GKE — use `gke-gvisor` there)       |
| `manifests/overlays/gke-gvisor`   | Extends `gvisor` with non-root securityContext, dropped caps, RuntimeDefault seccomp, node selector + toleration | GKE with the agent-sandbox addon enabled (Autopilot or Standard)                  |
| **Your own overlay**              | Copy `overlays/gvisor` and change the value                                                         | Any other `RuntimeClass` (`kata-containers`, `kata-qemu`, custom)                 |

Apply with `kubectl apply -k <path>`. Preview what will be applied
with `kubectl kustomize <path>`.

To add an overlay for, say, `kata-qemu`:

```bash
cp -r manifests/overlays/gvisor manifests/overlays/kata-qemu
sed -i 's/gvisor/kata-qemu/g' manifests/overlays/kata-qemu/{kustomization.yaml,runtime-class-patch.yaml}
kubectl apply -k manifests/overlays/kata-qemu
```

[gke-sandbox]: https://cloud.google.com/kubernetes-engine/docs/how-to/sandbox-pods

---

## Warm pool

By default, claiming a sandbox triggers pod scheduling +
readiness-probe wait. Observed cold-start times:

- **kind**: ~5–10s
- **GKE Standard** with capacity already in the gVisor node pool:
  ~5–10s
- **GKE Autopilot** (first claim of the session): up to **~50s**
  — Autopilot autoprovisions nodes on demand and pulls the image
  before the pod runs

A `SandboxWarmPool` keeps N pre-warmed sandboxes ready so claims
complete in ~1s when a pool slot is available — most valuable on
Autopilot where you're effectively pre-paying the autoprovisioning
cost:

```bash
kubectl apply -f manifests/warmpool.yaml
kubectl get sandboxwarmpool                    # Ready column should reach the desired replica count
kubectl get pods -l '!sandbox'                 # the pre-warmed pods are visible
```

Tune the pool size by editing `spec.replicas` in `warmpool.yaml` (or
`kubectl scale sandboxwarmpool/go-runtime-warmpool --replicas=N`).
Pick something around your expected concurrent-session count, with a
bit of headroom for replenishment.

When the demo or MCP server creates a `SandboxClaim`, the controller
fulfills it from the pool when a slot is available and starts warming
a replacement in the background.

Delete the pool with `kubectl delete -f manifests/warmpool.yaml` —
already-claimed sandboxes are unaffected; only the unclaimed warm
slots are removed.

---

## Running your own Go code

The samples that ship are wired into the demo via `samples.LoadModule`.
To exercise the sandbox with arbitrary code without modifying the demo,
write a small program against `pkg/goruntime` directly:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/gke-demos/go-runtime-sandbox/pkg/goruntime"
)

func main() {
    ctx := context.Background()
    rt, err := goruntime.Open(ctx, goruntime.Options{
        Namespace: "default",
        Template:  "go-runtime-template",
    })
    if err != nil { log.Fatal(err) }
    defer rt.Close(ctx)

    res, err := rt.Execute(ctx, goruntime.Request{
        Files: map[string][]byte{
            "go.mod":  []byte("module example.com/x\ngo 1.26\n"),
            "main.go": []byte(`package main
import "fmt"
func main(){ fmt.Println("anything goes") }
`),
        },
        Command: "go run main.go",
    })
    if err != nil { log.Fatal(err) }
    fmt.Println(res.Stdout)
}
```

Multi-file works the same way — just add keys with `/` to the `Files`
map; the library tar-streams them under the hood.

---

## Troubleshooting

**`502: Could not connect to the backend sandbox`** from the router.
The `SandboxTemplate` is missing `spec.service: true`, so the
controller didn't create the per-sandbox headless Service the router
resolves by DNS. Add it and re-apply.

**`go: cannot write multiple packages to non-directory app`** during a
build inside the sandbox. The `-o <file>` form requires a single
package; use `go build -o app .` (main package only), not
`go build -o app ./...`.

**`pattern all:_examples: cannot embed directory`** when building the
demo. Some file under `_examples/` accidentally got a `go.mod` —
embed refuses to cross nested module boundaries. Remove the nested
`go.mod`.

**`X-Sandbox-ID header is required`** in router logs. The client isn't
adding the routing headers — this usually means you're pointing
`APIURL` directly at the router from outside the cluster instead of
letting the client port-forward through it. Use the default
(port-forward) mode.

**Sandbox pod never becomes Ready.** Check the pod's events
(`kubectl describe pod -l sandbox=go-runtime-sandbox`) for image-pull
errors. On GKE that almost always means the image isn't in a registry
the cluster can pull from, or Workload Identity isn't configured for
the node pool to read from your Artifact Registry repo.

---

## Cleanup

Per-test resources (warmpool, template, router, stray claims):

```bash
kubectl delete -f manifests/warmpool.yaml --ignore-not-found
kubectl delete -k manifests/base --ignore-not-found     # or the overlay you applied
kubectl delete deployment sandbox-router-deployment --ignore-not-found
kubectl delete service sandbox-router-svc --ignore-not-found
kubectl delete sandboxclaims --all                       # any stragglers from interrupted runs
```

Uninstalling the agent-sandbox platform itself:

- **kind**: `kubectl delete -f` the upstream `extensions.yaml` and
  `manifest.yaml` you installed in
  [`scripts/run-test-kind.sh`](../scripts/run-test-kind.sh), or just
  delete the whole cluster (next step).
- **GKE**: disable the addon —
  `gcloud beta container clusters update YOUR_CLUSTER --location=YOUR_REGION --no-enable-agent-sandbox`.
  Don't `kubectl delete` the CRDs; the addon manages them.

kind cluster:

```bash
kind delete cluster --name agent-sandbox-poc
```

GKE cluster — delete via `gcloud` or the console when you're done with
it; the demo doesn't leave anything outside the cluster.

---

## Field notes

Lessons from running the smoke test against real clusters. Update this
section as you hit (or rule out) new issues.

### Validated configurations

| Cluster                         | Result   | Notes                                                                                                  |
| ------------------------------- | -------- | ------------------------------------------------------------------------------------------------------ |
| kind 0.31 / k8s 1.34            | ✅ pass  | Base overlay; no sandbox runtime; `manifest.yaml` + `extensions.yaml` install                          |
| GKE Autopilot 1.36.0-gke.1759000 | ✅ pass | `gke-gvisor` overlay; addon-installed CRDs; gVisor confirmed active (`runtimeClassName=gvisor`)        |

### GKE addon ships an older CRD than upstream v0.4.6

GKE's bundled `SandboxTemplate` only accepts three top-level spec
fields: `networkPolicy`, `networkPolicyManagement`, `podTemplate`.
The `spec.service` toggle that we use in `manifests/base` (and that
upstream v0.4.6 added for backward compat) is rejected with
`unknown field "spec.service"`. The `gke-gvisor` overlay strips it
with a `remove` op — apply that overlay and you're fine; apply the
base directly and you'll hit the error.

Similarly, `spec.envVarsInjectionPolicy` exists upstream but not on
GKE. If you add fields to the base manifest, check both APIs.

### GKE Autopilot tolerates the gVisor nodeSelector

The `gke-gvisor` overlay sets `nodeSelector: sandbox.gke.io/runtime:
gvisor` and a matching toleration — required on GKE Standard to land
on the gVisor node pool. On Autopilot the autoprovisioned gVisor
nodes carry the same label, so the selector is a no-op rather than
unschedulable. Verified by running the smoke test on Autopilot
with the overlay unmodified.

### Quick gVisor verification

To confirm pods are actually being sandboxed by gVisor rather than
running on the host kernel:

```bash
kubectl -n <NS> get pod <sandbox-pod> -o jsonpath='{.spec.runtimeClassName}{"\n"}'
# expect: gvisor

kubectl -n <NS> exec <sandbox-pod> -- dmesg 2>/dev/null | head -5
# gVisor's kernel prints distinctive lines; bare runc would show host dmesg
```

### `gcloud describe` flag for the addon is unreliable

`--format='value(addonsConfig.agentSandboxConfig.enabled)'` returns
an empty string (rendered as just whitespace) when the addon isn't
enabled — not `False`. Combined with multi-value tab-separated output,
it's easy to misread an empty for a True from a neighboring field.
Trust `kubectl get crd | grep agents.x-k8s.io` as the authoritative
signal that the addon installed correctly.

### Cold-start cost: ~50s on Autopilot, ~5–10s elsewhere

First-call latency on GKE Autopilot was ~50s in the smoke test:
node autoprovisioning + image pull + first `go run` (which populates
the module cache). Subsequent calls in the same sandbox: ~1–2s.
Subsequent claims from a `SandboxWarmPool`: also ~1–2s (pre-warmed
pods skip the autoprovisioning step). For anything an agent will
poke at interactively, apply `manifests/warmpool.yaml` early.

### Cleaning up GKE in one shot

`kubectl delete namespace <ns>` sweeps every per-test resource
together — `SandboxClaim`s, `Sandbox`es, router, template, warmpool.
Faster and harder to mess up than deleting individual resource types.
Do this from a dedicated namespace per smoke test (e.g. `go-runtime-demo`)
rather than from `default` to keep the blast radius bounded.
