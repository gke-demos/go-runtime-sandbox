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

// Package goruntime exposes a "ship some Go files and run a command"
// workflow on top of the agent-sandbox Go client. It backs both the CLI
// demo and any future agent-tool wrapper (MCP server, Anthropic SDK
// tool, etc.); the design's §5a covers the rationale for that split.
package goruntime

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/agent-sandbox/clients/go/sandbox"
)

// Options configures how a Session attaches to (or creates) a sandbox.
type Options struct {
	Namespace string
	Template  string
	// ClaimName, if non-empty, reattaches to an existing sandbox claim.
	// Empty means create a new one.
	ClaimName string
	// Client is optional; if nil, one is built from Namespace + Template.
	Client *sandbox.Client
	// Truncate controls LLM-friendly truncation of Result stdout/stderr.
	// Zero value applies defaults (8 KiB head + 8 KiB tail).
	Truncate TruncateConfig
}

// Request is a single execution: drop files under /app, run a shell
// command in /app, get a Result back.
type Request struct {
	// Files maps destination path (may contain "/") to file contents.
	// Files not listed here are left alone; /app persists across calls.
	Files map[string][]byte
	// Command is run via "sh -c" inside the sandbox.
	Command string
	// Timeout bounds this single Execute call. Zero = library default.
	Timeout time.Duration
}

// Result is what a command produced, post-truncation.
type Result struct {
	Stdout          string
	Stderr          string
	ExitCode        int
	Duration        time.Duration
	StdoutTruncated bool
	StderrTruncated bool
}

// Session is an open connection to a sandbox.
type Session struct {
	client    *sandbox.Client
	sb        *sandbox.Sandbox
	truncate  TruncateConfig
	ownClient bool
}

const (
	defaultExecuteTimeout = 5 * time.Minute
	defaultHeadBytes      = 8192
	defaultTailBytes      = 8192
)

// Open creates a Session either by creating a new sandbox (ClaimName == "")
// or reattaching to an existing claim. The Client is built from Namespace
// + Template if not provided.
func Open(ctx context.Context, opts Options) (*Session, error) {
	if opts.Namespace == "" {
		opts.Namespace = "default"
	}
	if opts.Template == "" {
		return nil, fmt.Errorf("goruntime: Template is required")
	}

	client := opts.Client
	ownClient := false
	if client == nil {
		c, err := sandbox.NewClient(ctx, sandbox.Options{
			TemplateName: opts.Template,
			Namespace:    opts.Namespace,
		})
		if err != nil {
			return nil, fmt.Errorf("goruntime: new client: %w", err)
		}
		client = c
		ownClient = true
	}

	var sb *sandbox.Sandbox
	var err error
	if opts.ClaimName != "" {
		sb, err = client.GetSandbox(ctx, opts.ClaimName, opts.Namespace)
		if err != nil {
			return nil, fmt.Errorf("goruntime: reattach %q: %w", opts.ClaimName, err)
		}
	} else {
		sb, err = client.CreateSandbox(ctx, opts.Template, opts.Namespace)
		if err != nil {
			return nil, fmt.Errorf("goruntime: create sandbox: %w", err)
		}
	}

	tc := opts.Truncate
	if tc.HeadBytes == 0 && tc.TailBytes == 0 {
		tc.HeadBytes = defaultHeadBytes
		tc.TailBytes = defaultTailBytes
	}

	return &Session{
		client:    client,
		sb:        sb,
		truncate:  tc,
		ownClient: ownClient,
	}, nil
}

// ClaimName returns the sandbox claim name. Persist this if you want to
// reattach in a future Open() call.
func (s *Session) ClaimName() string { return s.sb.ClaimName() }

// Reset wipes the sandbox working directory (/app) while preserving
// the Go build cache and module cache. Use between logical operations
// when you want a clean filesystem without paying for a new sandbox.
// Returns an error if the underlying shell call fails; non-zero exit
// from the rm itself (e.g. "no matches") is treated as success.
func (s *Session) Reset(ctx context.Context) error {
	if _, err := s.sb.Run(ctx, "rm -rf -- * .[!.]* 2>/dev/null; true"); err != nil {
		return fmt.Errorf("goruntime: reset: %w", err)
	}
	return nil
}

// Disconnect drops the network connection but leaves the sandbox alive.
// Use when handing the ClaimName back to a caller for later reuse.
func (s *Session) Disconnect(ctx context.Context) error {
	return s.sb.Disconnect(ctx)
}

// Close deletes the sandbox claim, tearing the sandbox down.
func (s *Session) Close(ctx context.Context) error {
	err := s.sb.Close(ctx)
	if s.ownClient {
		s.client.DeleteAll(ctx)
	}
	return err
}
