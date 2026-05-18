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

package goruntime

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/agent-sandbox/clients/go/sandbox"
)

// tarUploadName is the staged name for the multi-file tar payload, chosen
// to avoid collisions with anything an agent would plausibly upload.
const tarUploadName = ".goruntime-upload.tar"

// Execute materializes req.Files under /app and runs req.Command via
// "sh -c". Files not in req.Files are left untouched; /app persists
// across calls so build caches and prior artifacts survive.
//
// Routing: zero files or all keys at the workdir root use the
// agent-sandbox client's Write directly; any key containing "/" triggers
// the tar path (build in-memory, single Write of the archive, server-side
// "tar -xf" + remove).
func (s *Session) Execute(ctx context.Context, req Request) (*Result, error) {
	if req.Command == "" && len(req.Files) == 0 {
		return nil, fmt.Errorf("goruntime: Request must set Command or Files")
	}

	timeout := req.Timeout
	if timeout == 0 {
		timeout = defaultExecuteTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	if err := s.materialize(callCtx, req.Files); err != nil {
		return nil, err
	}
	if req.Command == "" {
		return &Result{Duration: time.Since(start)}, nil
	}

	raw, err := s.sb.Run(callCtx, req.Command, sandbox.WithTimeout(timeout))
	if err != nil {
		return nil, fmt.Errorf("goruntime: run: %w", err)
	}
	stdout, outTrunc := truncate(raw.Stdout, s.truncate)
	stderr, errTrunc := truncate(raw.Stderr, s.truncate)
	return &Result{
		Stdout:          stdout,
		Stderr:          stderr,
		ExitCode:        raw.ExitCode,
		Duration:        time.Since(start),
		StdoutTruncated: outTrunc,
		StderrTruncated: errTrunc,
	}, nil
}

func (s *Session) materialize(ctx context.Context, files map[string][]byte) error {
	if len(files) == 0 {
		return nil
	}
	for name := range files {
		if err := validatePath(name); err != nil {
			return fmt.Errorf("goruntime: %w", err)
		}
	}

	if !needsTar(files) {
		for name, content := range files {
			if err := s.sb.Write(ctx, name, content); err != nil {
				return fmt.Errorf("goruntime: write %q: %w", name, err)
			}
		}
		return nil
	}

	archive, err := buildTar(files)
	if err != nil {
		return fmt.Errorf("goruntime: build tar: %w", err)
	}
	if err := s.sb.Write(ctx, tarUploadName, archive); err != nil {
		return fmt.Errorf("goruntime: upload tar: %w", err)
	}
	// Extract under /app and remove the archive. sh -c so the && chains.
	cmd := fmt.Sprintf("tar -xf %s && rm -f %s", tarUploadName, tarUploadName)
	res, err := s.sb.Run(ctx, cmd, sandbox.WithTimeout(60*time.Second))
	if err != nil {
		return fmt.Errorf("goruntime: tar extract: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("goruntime: tar extract failed (exit %d): %s", res.ExitCode, res.Stderr)
	}
	return nil
}
