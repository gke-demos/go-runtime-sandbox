# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

#!/usr/bin/env bash
#
# End-to-end test on a local kind cluster. Builds and loads:
#   - the agent-sandbox controller (via published release manifests)
#   - the sandbox-router (built from the agent-sandbox repo at v0.4.6)
#   - the go-runtime-sandbox image (this repo)
# Applies the SandboxTemplate, then runs cmd/demo against the cluster.
#
# The cluster is reused across runs (the trap deletes per-test resources
# only). To start clean, run: kind delete cluster --name "$KIND_CLUSTER".

set -euo pipefail

KIND_CLUSTER="${KIND_CLUSTER:-agent-sandbox-poc}"
AS_VERSION="${AS_VERSION:-v0.4.6}"
ROUTER_IMG="sandbox-router:${AS_VERSION}"
SB_IMG="go-runtime-sandbox:latest"
NS="${NS:-default}"
ROUTER_CTX="https://github.com/kubernetes-sigs/agent-sandbox.git#${AS_VERSION}:clients/python/agentic-sandbox-client/sandbox-router"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

step() { printf '\n\033[1;34m==> %s\033[0m\n' "$*"; }

cleanup() {
  step "cleanup (per-test resources only; kind cluster preserved)"
  kubectl delete -k manifests/base --ignore-not-found --wait=false || true
  kubectl -n "$NS" delete deployment sandbox-router-deployment --ignore-not-found --wait=false || true
  kubectl -n "$NS" delete service sandbox-router-svc --ignore-not-found --wait=false || true
}
trap cleanup EXIT

step "ensure kind cluster '$KIND_CLUSTER' exists"
if ! kind get clusters | grep -qx "$KIND_CLUSTER"; then
  kind create cluster --name "$KIND_CLUSTER"
fi
kubectl config use-context "kind-${KIND_CLUSTER}"

step "install agent-sandbox controller ${AS_VERSION}"
kubectl apply -f "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AS_VERSION}/manifest.yaml"
kubectl apply -f "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AS_VERSION}/extensions.yaml"

step "wait for controller readiness"
kubectl -n agent-sandbox-system rollout status deployment/agent-sandbox-controller --timeout=180s
# Extensions controller may be in a different deployment if extensions are split out.
kubectl -n agent-sandbox-system get deployments -o name | xargs -r -I{} \
  kubectl -n agent-sandbox-system rollout status {} --timeout=180s

step "build sandbox-router image (${ROUTER_IMG})"
docker build -t "$ROUTER_IMG" "$ROUTER_CTX"

step "load sandbox-router image into kind"
kind load docker-image "$ROUTER_IMG" --name "$KIND_CLUSTER"

step "deploy sandbox-router into namespace '$NS'"
curl -sfL "https://raw.githubusercontent.com/kubernetes-sigs/agent-sandbox/${AS_VERSION}/clients/python/agentic-sandbox-client/sandbox-router/sandbox_router.yaml" \
  | sed -e "s|\${ROUTER_IMAGE}|$ROUTER_IMG|g" \
        -e "s|# imagePullPolicy: Never|imagePullPolicy: IfNotPresent|" \
  | kubectl -n "$NS" apply -f -
kubectl -n "$NS" rollout status deployment/sandbox-router-deployment --timeout=180s

step "build go-runtime-sandbox image"
docker build -t "$SB_IMG" "$REPO_ROOT"

step "load go-runtime-sandbox image into kind"
kind load docker-image "$SB_IMG" --name "$KIND_CLUSTER"

step "apply SandboxTemplate (base — no runtimeClassName; kind has no sandbox runtime)"
kubectl apply -k manifests/base
# Wait briefly for the CRD-backed resource to be visible to the API.
for _ in {1..30}; do
  if kubectl -n "$NS" get sandboxtemplate go-runtime-template >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

step "run demo (both flows)"
go run ./cmd/demo --namespace="$NS" --template=go-runtime-template --flow=all
