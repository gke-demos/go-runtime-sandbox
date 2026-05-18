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
	"archive/tar"
	"bytes"
	"io"
	"testing"
)

func TestNeedsTar(t *testing.T) {
	cases := []struct {
		name  string
		files map[string][]byte
		want  bool
	}{
		{"empty", map[string][]byte{}, false},
		{"flat one", map[string][]byte{"main.go": nil}, false},
		{"flat several", map[string][]byte{"main.go": nil, "go.mod": nil}, false},
		{"with subdir", map[string][]byte{"main.go": nil, "greet/greet.go": nil}, true},
		{"only subdir", map[string][]byte{"a/b/c.txt": nil}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsTar(tc.files); got != tc.want {
				t.Fatalf("needsTar(%v) = %v, want %v", tc.files, got, tc.want)
			}
		})
	}
}

func TestValidatePath(t *testing.T) {
	bad := []string{"", "/etc/passwd", "..", "../escape", "ok/../escape", "ok//bad", "ok/./bad", "ok/"}
	for _, p := range bad {
		if err := validatePath(p); err == nil {
			t.Errorf("validatePath(%q) accepted, want error", p)
		}
	}
	good := []string{"main.go", "go.mod", "greet/greet.go", "a/b/c/d.txt"}
	for _, p := range good {
		if err := validatePath(p); err != nil {
			t.Errorf("validatePath(%q) rejected: %v", p, err)
		}
	}
}

func TestParentDirs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"main.go", nil},
		{"a/b.go", []string{"a"}},
		{"a/b/c.go", []string{"a", "a/b"}},
	}
	for _, tc := range cases {
		got := parentDirs(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("parentDirs(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("parentDirs(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestBuildTarRoundTrip(t *testing.T) {
	files := map[string][]byte{
		"go.mod":          []byte("module example.com/m\ngo 1.26\n"),
		"main.go":         []byte("package main\nfunc main(){}\n"),
		"greet/greet.go":  []byte("package greet\nfunc Hello() string { return \"hi\" }\n"),
		"deep/a/b/x.txt":  []byte("x"),
	}
	archive, err := buildTar(files)
	if err != nil {
		t.Fatalf("buildTar: %v", err)
	}

	r := tar.NewReader(bytes.NewReader(archive))
	seenFiles := map[string][]byte{}
	seenDirs := map[string]bool{}
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			seenDirs[h.Name] = true
		case tar.TypeReg:
			b, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("tar.Read %q: %v", h.Name, err)
			}
			seenFiles[h.Name] = b
		default:
			t.Fatalf("unexpected typeflag %v for %q", h.Typeflag, h.Name)
		}
	}

	if len(seenFiles) != len(files) {
		t.Fatalf("file count: got %d (%v), want %d", len(seenFiles), keys(seenFiles), len(files))
	}
	for name, want := range files {
		got, ok := seenFiles[name]
		if !ok {
			t.Errorf("missing file %q in archive", name)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("file %q contents differ", name)
		}
	}
	for _, dir := range []string{"greet/", "deep/", "deep/a/", "deep/a/b/"} {
		if !seenDirs[dir] {
			t.Errorf("missing dir %q in archive", dir)
		}
	}
}

func TestBuildTarDeterministic(t *testing.T) {
	files := map[string][]byte{
		"b/y.txt": []byte("y"),
		"a/x.txt": []byte("x"),
	}
	a1, _ := buildTar(files)
	a2, _ := buildTar(files)
	if !bytes.Equal(a1, a2) {
		t.Fatalf("buildTar not deterministic across calls")
	}
}

func TestBuildTarRejectsInvalidPaths(t *testing.T) {
	if _, err := buildTar(map[string][]byte{"../escape": nil}); err == nil {
		t.Fatal("expected error for traversal path")
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
