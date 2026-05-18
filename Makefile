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

# Image URL used by `make image` and `make image-push`. Override on the
# command line, e.g. `make image IMG=ghcr.io/me/go-runtime-sandbox:dev`.
IMG ?= ghcr.io/gke-demos/go-runtime-sandbox:latest

# Container tool. Override with podman if you don't have docker.
CONTAINER_TOOL ?= docker

SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Build

.PHONY: build
build: ## Build all Go binaries into bin/.
	@mkdir -p bin
	go build -o bin/mcp-server ./cmd/mcp-server
	go build -o bin/demo ./cmd/demo
	go build -o bin/mcp-smoke-test ./cmd/mcp-smoke-test

.PHONY: mcp-server
mcp-server: ## Build just cmd/mcp-server into bin/.
	@mkdir -p bin
	go build -o bin/mcp-server ./cmd/mcp-server

.PHONY: image
image: ## Build the runtime sandbox container image.
	$(CONTAINER_TOOL) build -t $(IMG) .

.PHONY: image-push
image-push: ## Push the runtime sandbox container image.
	$(CONTAINER_TOOL) push $(IMG)

##@ Test

.PHONY: test
test: ## Run unit tests + go vet.
	go vet ./...
	go test ./...

.PHONY: lint
lint: ## Run golangci-lint (install: `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`).
	@which golangci-lint > /dev/null || { echo 'install golangci-lint first: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest'; exit 1; }
	golangci-lint run ./...

.PHONY: tidy
tidy: ## Verify go.mod and go.sum are tidy (CI: ./hack/verify-tidy.sh).
	go mod tidy
	@git diff --exit-code go.mod go.sum || { echo 'go.mod/go.sum changed after `go mod tidy`'; exit 1; }

.PHONY: e2e-kind
e2e-kind: ## Run the kind end-to-end test (slow; requires Docker + kind + kubectl).
	./scripts/run-test-kind.sh

##@ Docs

.PHONY: docs-serve
docs-serve: ## Serve the Hugo docs site locally on :1313.
	@cd docs/site && npm install --silent && hugo server

.PHONY: docs-build
docs-build: ## Build the Hugo docs site into docs/site/public.
	@cd docs/site && npm install --silent && hugo --minify

##@ Cleanup

.PHONY: clean
clean: ## Remove build artifacts.
	rm -rf bin/ docs/site/public docs/site/resources
