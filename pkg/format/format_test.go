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

package format

import (
	"strings"
	"testing"
	"time"

	"github.com/gke-demos/go-runtime-sandbox/pkg/goruntime"
)

func TestResult_HappyPath(t *testing.T) {
	out := Result(&goruntime.Result{
		Stdout:   "hello\n",
		ExitCode: 0,
		Duration: 47 * time.Millisecond,
	})
	if !strings.HasPrefix(out, "exit=0") {
		t.Fatalf("header missing: %q", out)
	}
	if !strings.Contains(out, "── stdout ──\nhello\n") {
		t.Fatalf("stdout block malformed: %q", out)
	}
	if strings.Contains(out, "stderr") {
		t.Fatalf("stderr block leaked: %q", out)
	}
}

func TestResult_NonZeroExit(t *testing.T) {
	out := Result(&goruntime.Result{
		Stderr:   "panic: bad\n",
		ExitCode: 2,
		Duration: 12 * time.Millisecond,
	})
	if !strings.HasPrefix(out, "exit=2") {
		t.Fatalf("exit code missing: %q", out)
	}
	if !strings.Contains(out, "── stderr ──\npanic: bad\n") {
		t.Fatalf("stderr block malformed: %q", out)
	}
}

func TestResult_Truncation(t *testing.T) {
	out := Result(&goruntime.Result{
		Stdout:          "head\n[elided]\ntail\n",
		StdoutTruncated: true,
		ExitCode:        0,
	})
	if !strings.Contains(out, "[truncated: stdout]") {
		t.Fatalf("truncation marker missing: %q", out)
	}
}

func TestResult_BothStreamsTruncated(t *testing.T) {
	out := Result(&goruntime.Result{
		StdoutTruncated: true,
		StderrTruncated: true,
		Stdout:          "x",
		Stderr:          "y",
	})
	if !strings.Contains(out, "[truncated: stdout, stderr]") {
		t.Fatalf("combined truncation marker missing: %q", out)
	}
}

func TestResult_NoOutput(t *testing.T) {
	out := Result(&goruntime.Result{ExitCode: 0})
	if !strings.Contains(out, "(no output)") {
		t.Fatalf("missing 'no output' marker: %q", out)
	}
}

func TestResult_NoTrailingNewlineAdded(t *testing.T) {
	// stdout already ends with newline; we shouldn't add another.
	out := Result(&goruntime.Result{Stdout: "one\ntwo\n"})
	if strings.Contains(out, "two\n\n") {
		t.Fatalf("double-newline introduced: %q", out)
	}
}
