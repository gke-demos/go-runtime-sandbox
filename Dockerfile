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

# syntax=docker/dockerfile:1.7

# --platform=$BUILDPLATFORM pins the Go stages to the host arch (e.g.
# linux/amd64 on a typical CI runner), so multi-arch builds (`docker
# buildx --platform linux/amd64,linux/arm64`) cross-compile via the Go
# toolchain instead of running the entire Go build under QEMU
# emulation. Without this, the arm64 leg takes ~20 min vs ~2 min.
# The runtime stage and the toolchain-source stage stay per-target so
# their contents (apt packages, /usr/local/go layout) match the
# destination arch.

# ── Stage 1: source of /usr/local/go to ship into the runtime ────────
# Per-target-arch; just a COPY source, no code execution.
FROM golang:1.26-bookworm AS toolchain

# ── Stage 2: build sandbox-server + pre-warm $GOCACHE for the target ─
# Pinned to BUILDPLATFORM. Go cross-compiles to TARGETOS/TARGETARCH;
# the GOCACHE entries we produce here are keyed on those values, so
# they're valid when the runtime sandbox later does `go build`
# natively on the target arch.
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS server-build
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY server/ ./server/
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/sandbox-server ./server

# Pre-warm $GOCACHE with the most-used stdlib packages so the first
# `go build` / `go run` inside a fresh sandbox skips ~10s of stdlib
# compilation. Transitively pulls in dozens of packages via net/http
# and friends. Costs ~20s at image build time; saves it back on every
# first call thereafter. Cross-compiled to TARGETARCH so the cache is
# usable when the runtime sandbox does `go build` natively.
RUN mkdir /tmp/warm && cd /tmp/warm \
 && go mod init warm \
 && printf 'package main\nimport (\n\t_ "context"\n\t_ "encoding/json"\n\t_ "errors"\n\t_ "fmt"\n\t_ "io"\n\t_ "log"\n\t_ "log/slog"\n\t_ "math"\n\t_ "net/http"\n\t_ "os"\n\t_ "path/filepath"\n\t_ "regexp"\n\t_ "sort"\n\t_ "strconv"\n\t_ "strings"\n\t_ "sync"\n\t_ "testing"\n\t_ "time"\n)\nfunc main(){}\n' > main.go \
 && CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} GOCACHE=/out/gocache \
    go build -o /dev/null . \
 && cd / && rm -rf /tmp/warm

# ── Stage 3: slim runtime ────────────────────────────────────────────
# Per-target-arch. apt-get and useradd run under QEMU on the non-native
# leg but they're cheap; the heavy Go work was done in stage 2.
FROM debian:bookworm-slim AS runtime
RUN apt-get update \
 && apt-get install -y --no-install-recommends tar ca-certificates git \
 && rm -rf /var/lib/apt/lists/*
COPY --from=toolchain /usr/local/go /usr/local/go
COPY --from=server-build /out/sandbox-server /usr/local/bin/sandbox-server
RUN useradd -m -u 1000 -s /bin/bash sandbox \
 && mkdir -p /app /home/sandbox/.cache/go-build /home/sandbox/go/pkg/mod \
 && chown -R 1000:1000 /app /home/sandbox
COPY --chown=1000:1000 --from=server-build /out/gocache/ /home/sandbox/.cache/go-build/
USER 1000
WORKDIR /app
ENV PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin \
    GOCACHE=/home/sandbox/.cache/go-build \
    GOMODCACHE=/home/sandbox/go/pkg/mod \
    GOTOOLCHAIN=local \
    HOME=/home/sandbox \
    CGO_ENABLED=0 \
    GOFLAGS=-buildvcs=false

EXPOSE 8888
CMD ["sandbox-server"]
