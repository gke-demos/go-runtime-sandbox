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

// Package samples exposes the embedded /_examples tree to anything in
// this module that needs it (today: cmd/demo). It lives at the project
// root because //go:embed cannot use ".." patterns, so the embedder
// must sit at or above the embedded directory.
//
// We synthesize each sample's go.mod here rather than committing one
// under _examples/: a real go.mod would mark the sample dir as a nested
// module, which Go's embed mechanism refuses to traverse.
package samples

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed all:_examples
var fsys embed.FS

const goVersion = "1.26"

// goModFor returns the go.mod content for the given sample name.
var goModFor = map[string]string{
	"smoke":     "module example.com/smoke\n\ngo " + goVersion + "\n",
	"multifile": "module example.com/multifile\n\ngo " + goVersion + "\n",
}

// LoadModule returns the files of the named sample as a path->bytes map
// keyed by paths relative to the sample's root (e.g. "main.go",
// "greet/greet.go") and a synthesized "go.mod", suitable for passing to
// goruntime.Request.Files.
func LoadModule(name string) (map[string][]byte, error) {
	root := "_examples/" + name
	out := map[string][]byte{}
	err := fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(path, root+"/")
		b, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		out[rel] = b
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("samples: load %q: %w", name, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("samples: %q is empty", name)
	}
	if mod, ok := goModFor[name]; ok {
		out["go.mod"] = []byte(mod)
	} else {
		return nil, fmt.Errorf("samples: no go.mod template for %q", name)
	}
	return out, nil
}
