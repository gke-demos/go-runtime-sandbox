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

package main

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

const maxOutputBytes = 8 << 20

// runShell executes command via "sh -c" with cwd = workdir, capturing
// stdout/stderr into separate buffers. Each stream is tail-truncated at
// maxOutputBytes; the dumb wire-level backstop. LLM-friendly head+tail
// truncation lives in pkg/goruntime (design §5a), not here.
func runShell(ctx context.Context, workdir, command string) (stdout, stderr string, exitCode int) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = workdir

	var outBuf, errBuf cappedBuffer
	outBuf.cap = maxOutputBytes
	errBuf.cap = maxOutputBytes
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
			if errBuf.Len() == 0 {
				_, _ = errBuf.Write([]byte(err.Error()))
			}
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

type cappedBuffer struct {
	buf       bytes.Buffer
	cap       int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := c.cap - c.buf.Len()
	if remaining <= 0 {
		c.truncated = true
		return n, nil
	}
	if len(p) > remaining {
		c.buf.Write(p[:remaining])
		c.truncated = true
		return n, nil
	}
	c.buf.Write(p)
	return n, nil
}

func (c *cappedBuffer) Len() int { return c.buf.Len() }

func (c *cappedBuffer) String() string {
	if c.truncated {
		return c.buf.String() + "\n... [truncated]"
	}
	return c.buf.String()
}
