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

// Package format turns a goruntime.Result into a string sized and
// shaped for LLM consumption: exit code prominent, duration available,
// stdout/stderr cleanly separated, truncation marked.
package format

import (
	"fmt"
	"strings"

	"github.com/gke-demos/go-runtime-sandbox/pkg/goruntime"
)

// Result renders r as a human-and-LLM-friendly string. The first line
// is always a status header so a model can decide on the result without
// scrolling. Empty streams are omitted entirely (no "stdout: (empty)"
// noise).
func Result(r *goruntime.Result) string {
	var b strings.Builder

	fmt.Fprintf(&b, "exit=%d  (%s)", r.ExitCode, r.Duration.Round(1e6))
	if r.StdoutTruncated || r.StderrTruncated {
		fmt.Fprintf(&b, "  [truncated: %s]", truncatedLabel(r))
	}
	b.WriteByte('\n')

	if r.Stdout != "" {
		b.WriteString("\n── stdout ──\n")
		b.WriteString(ensureNewline(r.Stdout))
	}
	if r.Stderr != "" {
		b.WriteString("\n── stderr ──\n")
		b.WriteString(ensureNewline(r.Stderr))
	}
	if r.Stdout == "" && r.Stderr == "" {
		b.WriteString("\n(no output)\n")
	}
	return b.String()
}

func truncatedLabel(r *goruntime.Result) string {
	switch {
	case r.StdoutTruncated && r.StderrTruncated:
		return "stdout, stderr"
	case r.StdoutTruncated:
		return "stdout"
	default:
		return "stderr"
	}
}

func ensureNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
