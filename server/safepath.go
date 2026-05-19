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
	"errors"
	"net/url"
	"path/filepath"
	"strings"
)

var errAccessDenied = errors.New("access denied: path must be within workdir")

// resolveSafe decodes the percent-encoded URL segment, joins it onto workdir,
// resolves symlinks, and verifies the result is contained within workdir.
//
// The client's percent-encoding encodes every byte outside the RFC 3986
// unreserved set, so "/" arrives as "%2F"; we unescape it here and let the
// joined path naturally span subdirectories under workdir.
func resolveSafe(workdir, encoded string) (string, error) {
	requested, err := url.PathUnescape(encoded)
	if err != nil {
		return "", err
	}
	requested = strings.TrimLeft(requested, "/")
	if requested == "" {
		requested = "."
	}
	absWorkdir, err := filepath.Abs(workdir)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(absWorkdir, requested)

	// Resolve symlinks if the path exists; if it doesn't, resolve the
	// closest existing ancestor and re-append the remainder so traversal
	// attempts on non-existent paths still get caught.
	resolved, err := resolveExistingPrefix(joined)
	if err != nil {
		return "", err
	}
	if !withinDir(absWorkdir, resolved) {
		return "", errAccessDenied
	}
	return resolved, nil
}

func resolveExistingPrefix(p string) (string, error) {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real, nil
	}
	parent, leaf := filepath.Split(p)
	parent = strings.TrimRight(parent, string(filepath.Separator))
	if parent == "" || parent == p {
		return filepath.Abs(p)
	}
	resolvedParent, err := resolveExistingPrefix(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, leaf), nil
}

func withinDir(dir, target string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}
