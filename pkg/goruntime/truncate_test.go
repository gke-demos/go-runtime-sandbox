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
	"strings"
	"testing"
)

func TestTruncate(t *testing.T) {
	t.Run("short input passes through", func(t *testing.T) {
		out, trunc := truncate("hello", TruncateConfig{HeadBytes: 100, TailBytes: 100})
		if trunc {
			t.Fatalf("expected not truncated, got truncated")
		}
		if out != "hello" {
			t.Fatalf("output mismatch: %q", out)
		}
	})

	t.Run("zero config is passthrough", func(t *testing.T) {
		s := strings.Repeat("x", 100_000)
		out, trunc := truncate(s, TruncateConfig{})
		if trunc {
			t.Fatalf("expected not truncated with zero config")
		}
		if out != s {
			t.Fatalf("zero config altered output")
		}
	})

	t.Run("long input keeps head and tail", func(t *testing.T) {
		s := "AAAA" + strings.Repeat("M", 1000) + "ZZZZ"
		out, trunc := truncate(s, TruncateConfig{HeadBytes: 4, TailBytes: 4})
		if !trunc {
			t.Fatalf("expected truncated")
		}
		if !strings.HasPrefix(out, "AAAA") {
			t.Fatalf("missing head: %q", out[:20])
		}
		if !strings.HasSuffix(out, "ZZZZ") {
			t.Fatalf("missing tail: %q", out[len(out)-20:])
		}
		if !strings.Contains(out, "bytes elided") {
			t.Fatalf("missing elision marker: %q", out)
		}
	})

	t.Run("error survives at tail", func(t *testing.T) {
		s := "start of program output\n" +
			strings.Repeat("filler ", 5000) +
			"\npanic: runtime error: index out of range [3] with length 2\n"
		out, trunc := truncate(s, TruncateConfig{HeadBytes: 80, TailBytes: 200})
		if !trunc {
			t.Fatalf("expected truncated")
		}
		if !strings.Contains(out, "panic: runtime error") {
			t.Fatalf("error message lost in truncation: %q", out)
		}
	})

	t.Run("head-only", func(t *testing.T) {
		s := strings.Repeat("x", 1000)
		out, trunc := truncate(s, TruncateConfig{HeadBytes: 100})
		if !trunc {
			t.Fatalf("expected truncated")
		}
		if !strings.HasPrefix(out, strings.Repeat("x", 100)) {
			t.Fatalf("head missing")
		}
	})
}
