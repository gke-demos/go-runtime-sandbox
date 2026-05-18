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

// cmd/mcp-server exposes a single "run_go_code" tool over MCP (stdio
// transport). Each running server holds one Kubernetes Agent Sandbox,
// lazily created on first tool call and reused across calls — files
// and build cache persist within a session, which is exactly the
// property an iterating agent needs.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gke-demos/go-runtime-sandbox/pkg/format"
	"github.com/gke-demos/go-runtime-sandbox/pkg/goruntime"
)

const version = "0.1.0"

type config struct {
	namespace  string
	template   string
	claim      string
	persistent bool
	ephemeral  bool
	openTO     time.Duration
}

func main() {
	var cfg config
	flag.StringVar(&cfg.namespace, "namespace", "default", "Kubernetes namespace")
	flag.StringVar(&cfg.template, "template", "go-runtime-template", "SandboxTemplate name")
	flag.StringVar(&cfg.claim, "claim", "", "reattach to an existing sandbox by claim name (otherwise a new one is created on first tool call)")
	flag.BoolVar(&cfg.persistent, "persistent", false, "on shutdown, Disconnect (leave sandbox alive) instead of Close (delete it)")
	flag.BoolVar(&cfg.ephemeral, "ephemeral", false, "wipe /app before every tool call (build/module caches preserved); use for paranoid deployments that don't want state to leak between logical operations")
	flag.DurationVar(&cfg.openTO, "open-timeout", 5*time.Minute, "max time to spend opening the sandbox")
	flag.Parse()

	// Stderr is the only safe sink for logs — stdout carries MCP frames.
	log.SetOutput(os.Stderr)
	log.SetPrefix("mcp-server: ")
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	holder := &sessionHolder{cfg: cfg}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "go-runtime-sandbox",
		Version: version,
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "run_go_code",
		Description: toolDescription,
	}, holder.runGoCode)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runErr := server.Run(ctx, &mcp.StdioTransport{})

	// Tear down the sandbox after the transport closes. Use a fresh
	// context so this still completes if the parent was canceled.
	shutdownCtx, c := context.WithTimeout(context.Background(), 60*time.Second)
	defer c()
	if err := holder.shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}

	if runErr != nil && ctx.Err() == nil {
		log.Fatalf("server: %v", runErr)
	}
}

// RunGoCodeArgs is the input schema for the run_go_code tool. JSON
// schema tags drive the descriptions the LLM sees.
type RunGoCodeArgs struct {
	Files map[string]string `json:"files,omitempty" jsonschema:"files to write under /app before running the command, keyed by relative path (e.g. 'main.go', 'greet/greet.go'). Existing files not in this map are left alone. Omit to run a command against existing sandbox state."`
	Command string `json:"command" jsonschema:"shell command run via 'sh -c' in /app. Examples: 'go run main.go', 'go test ./...', 'go build -o app . && ./app'. State persists across calls."`
}

type sessionHolder struct {
	cfg config

	mu      sync.Mutex
	session *goruntime.Session

	// execMu serializes Execute calls so concurrent tool invocations
	// can't race on /app or clobber each other's build artifacts. The
	// MCP SDK may dispatch tool calls concurrently; this gives the
	// session the "one-at-a-time" semantics agents actually expect.
	execMu sync.Mutex
}

func (h *sessionHolder) ensureSession(ctx context.Context) (*goruntime.Session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.session != nil {
		return h.session, nil
	}
	openCtx, cancel := context.WithTimeout(ctx, h.cfg.openTO)
	defer cancel()
	log.Printf("opening sandbox (namespace=%s template=%s claim=%q)", h.cfg.namespace, h.cfg.template, h.cfg.claim)
	s, err := goruntime.Open(openCtx, goruntime.Options{
		Namespace: h.cfg.namespace,
		Template:  h.cfg.template,
		ClaimName: h.cfg.claim,
	})
	if err != nil {
		return nil, err
	}
	log.Printf("sandbox open: claim=%s", s.ClaimName())
	h.session = s
	return s, nil
}

func (h *sessionHolder) shutdown(ctx context.Context) error {
	h.mu.Lock()
	s := h.session
	h.session = nil
	h.mu.Unlock()
	if s == nil {
		return nil
	}
	if h.cfg.persistent {
		log.Printf("disconnecting (claim %s preserved)", s.ClaimName())
		return s.Disconnect(ctx)
	}
	log.Printf("closing (deleting sandbox %s)", s.ClaimName())
	return s.Close(ctx)
}

func (h *sessionHolder) runGoCode(ctx context.Context, _ *mcp.CallToolRequest, args RunGoCodeArgs) (*mcp.CallToolResult, any, error) {
	if args.Command == "" {
		return toolError("missing required 'command' field"), nil, nil
	}

	s, err := h.ensureSession(ctx)
	if err != nil {
		return toolError(fmt.Sprintf("could not open sandbox: %v", err)), nil, nil
	}

	h.execMu.Lock()
	defer h.execMu.Unlock()

	if h.cfg.ephemeral {
		if err := s.Reset(ctx); err != nil {
			return toolError(fmt.Sprintf("reset failed: %v", err)), nil, nil
		}
	}

	res, err := s.Execute(ctx, goruntime.Request{
		Files:   toBytes(args.Files),
		Command: args.Command,
	})
	if err != nil {
		return toolError(fmt.Sprintf("execute failed: %v", err)), nil, nil
	}

	text := format.Result(res)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
		// Surface non-zero exit as a tool error so the model knows to
		// react rather than treating the output as success.
		IsError: res.ExitCode != 0,
	}, nil, nil
}

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}
}

func toBytes(in map[string]string) map[string][]byte {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]byte, len(in))
	for k, v := range in {
		out[k] = []byte(v)
	}
	return out
}

const toolDescription = `Build and run Go code in an isolated Kubernetes sandbox.

How it works:
- Optionally write a set of files under /app (the sandbox's working directory). Paths may be nested (e.g. "greet/greet.go").
- Then run a shell command in /app via 'sh -c'.
- Get back the exit code, stdout, and stderr.

State persists across calls within this session: files you wrote stay,
the Go build cache stays warm, and binaries you compiled remain
executable. To start fresh, run a command like "rm -rf -- *".

Examples:
- {files: {"go.mod":"module x\ngo 1.26\n","main.go":"package main\nimport \"fmt\"\nfunc main(){fmt.Println(\"hi\")}\n"}, command: "go run main.go"}
- {command: "go build -o app . && ./app"}
- {files: {"greet/greet.go":"package greet\n..."}, command: "go test ./..."}
`
