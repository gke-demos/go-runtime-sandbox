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

// cmd/mcp-smoke-test spawns ./mcp-server as a subprocess, speaks MCP to it
// over stdio, and exercises run_go_code with a single-file and
// multi-file payload. Used to validate the server end-to-end against a
// running cluster without involving Claude Code.
//
//	go build -o /tmp/mcp-server ./cmd/mcp-server
//	go run ./cmd/mcp-smoke-test -server /tmp/mcp-server
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	serverPath := flag.String("server", "/tmp/mcp-server", "path to the mcp-server binary")
	namespace := flag.String("namespace", "default", "k8s namespace")
	template := flag.String("template", "go-runtime-template", "SandboxTemplate name")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "mcp-smoke-test", Version: "0.1.0"}, nil)
	transport := &mcp.CommandTransport{
		Command: exec.CommandContext(ctx, *serverPath,
			"--namespace="+*namespace,
			"--template="+*template,
		),
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		log.Fatalf("list tools: %v", err)
	}
	fmt.Printf("tools advertised: %d\n", len(tools.Tools))
	for _, t := range tools.Tools {
		fmt.Printf("  - %s: %s\n", t.Name, firstLine(t.Description))
	}

	check(call(ctx, session, "single-file go run", map[string]any{
		"files": map[string]string{
			"go.mod":  "module example.com/m\ngo 1.26\n",
			"main.go": "package main\nimport \"fmt\"\nfunc main(){ fmt.Println(\"hello from mcp smoke\") }\n",
		},
		"command": "go run main.go",
	}))

	check(call(ctx, session, "build + re-run binary", map[string]any{
		"command": "go build -o app main.go && ./app",
	}))

	check(call(ctx, session, "multi-file build+run", map[string]any{
		"files": map[string]string{
			"go.mod":     "module example.com/multi\ngo 1.26\n",
			"main.go":    "package main\nimport (\"fmt\"; \"example.com/multi/sub\")\nfunc main(){ fmt.Println(sub.Msg()) }\n",
			"sub/sub.go": "package sub\nfunc Msg() string { return \"hi from sub-package\" }\n",
		},
		"command": "rm -f app && go build -o app . && ./app",
	}))

	check(call(ctx, session, "intentional compile error", map[string]any{
		"files":   map[string]string{"main.go": "package main\nfunc main(){ broken }\n"},
		"command": "go run main.go",
	}))

	fmt.Println("\nsmoke complete")
}

func call(ctx context.Context, s *mcp.ClientSession, label string, args map[string]any) (*mcp.CallToolResult, error) {
	fmt.Printf("\n========== %s ==========\n", label)
	res, err := s.CallTool(ctx, &mcp.CallToolParams{
		Name:      "run_go_code",
		Arguments: args,
	})
	if err != nil {
		return nil, err
	}
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			fmt.Println(t.Text)
		}
	}
	if res.IsError {
		fmt.Println("(tool reported IsError=true)")
	}
	return res, nil
}

func check(_ *mcp.CallToolResult, err error) {
	if err != nil {
		log.Fatalf("call failed: %v", err)
	}
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
