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

# Stage 1: source of /usr/local/go to ship into the runtime
FROM golang:1.26-bookworm AS toolchain

# Stage 2: build the sandbox server binary
FROM golang:1.26-bookworm AS server-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY server/ ./server/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/sandbox-server ./server

# Stage 3: slim runtime — toolchain + server + supporting tools
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
    GOTOOLCHAIN=local \
    HOME=/home/sandbox \
    CGO_ENABLED=0 \
    GOFLAGS=-buildvcs=false

# Pre-warm $GOCACHE with the most-used stdlib packages so the first
# `go build` / `go run` inside a fresh sandbox skips ~10s of stdlib
# compilation. Transitively pulls in dozens of packages via net/http
# and friends. Costs ~20s at image build time; saves it back on every
# first call thereafter.
RUN mkdir /tmp/warm && cd /tmp/warm \
 && go mod init warm \
 && printf 'package main\nimport (\n\t_ "context"\n\t_ "encoding/json"\n\t_ "errors"\n\t_ "fmt"\n\t_ "io"\n\t_ "log"\n\t_ "log/slog"\n\t_ "math"\n\t_ "net/http"\n\t_ "os"\n\t_ "path/filepath"\n\t_ "regexp"\n\t_ "sort"\n\t_ "strconv"\n\t_ "strings"\n\t_ "sync"\n\t_ "testing"\n\t_ "time"\n)\nfunc main(){}\n' > main.go \
 && go build -o /dev/null . \
 && cd / && rm -rf /tmp/warm

EXPOSE 8888
CMD ["sandbox-server"]
