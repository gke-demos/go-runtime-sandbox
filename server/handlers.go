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
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxUploadSize = 256 << 20

func registerHandlers(mux *http.ServeMux, workdir string, log *slog.Logger) {
	mux.HandleFunc("GET /{$}", handleRoot)
	mux.HandleFunc("POST /execute", handleExecute(workdir, log))
	mux.HandleFunc("POST /upload", handleUpload(workdir, log))
	mux.HandleFunc("GET /download/", handleDownload(workdir))
	mux.HandleFunc("GET /list/", handleList(workdir))
	mux.HandleFunc("GET /exists/", handleExists(workdir))
}

func handleRoot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleExecute(workdir string, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Command string `json:"command"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
		if req.Command == "" {
			writeError(w, http.StatusBadRequest, "command must not be empty")
			return
		}
		preview := commandPreview(req.Command)
		log.Info("exec begin", "command", preview)
		start := time.Now()
		stdout, stderr, code := runShell(r.Context(), workdir, req.Command)
		log.Info("exec done",
			"command", preview,
			"exit", code,
			"stdout_bytes", len(stdout),
			"stderr_bytes", len(stderr),
			"duration_ms", time.Since(start).Milliseconds(),
		)
		writeJSON(w, http.StatusOK, map[string]any{
			"stdout":    stdout,
			"stderr":    stderr,
			"exit_code": code,
		})
	}
}

// commandPreview returns a single-line, length-capped form of cmd
// suitable for log lines — collapses newlines, clips to 120 chars
// with a trailing ellipsis.
func commandPreview(cmd string) string {
	s := strings.ReplaceAll(cmd, "\n", "⏎")
	const max = 120
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func handleUpload(workdir string, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "failed to parse multipart form: "+err.Error())
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, `missing "file" form field: `+err.Error())
			return
		}
		defer func() { _ = file.Close() }()

		name := filepath.Base(header.Filename)
		if name == "" || name == "." || name == ".." {
			writeError(w, http.StatusBadRequest, "invalid filename")
			return
		}
		dest := filepath.Join(workdir, name)
		out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create file: "+err.Error())
			return
		}
		defer func() { _ = out.Close() }()
		n, err := io.Copy(out, file)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to write file: "+err.Error())
			return
		}
		log.Debug("upload complete", "filename", name, "size", n)
		writeJSON(w, http.StatusOK, map[string]any{
			"filename": name,
			"size":     n,
		})
	}
}

func handleDownload(workdir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		encoded, ok := trimPrefixEscaped(r.URL.EscapedPath(), "/download/")
		if !ok {
			writeError(w, http.StatusNotFound, "missing path")
			return
		}
		path, err := resolveSafe(workdir, encoded)
		if err != nil {
			if errors.Is(err, errAccessDenied) {
				writeError(w, http.StatusForbidden, err.Error())
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "file not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if info.IsDir() {
			writeError(w, http.StatusBadRequest, "path is a directory")
			return
		}
		f, err := os.Open(path)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer func() { _ = f.Close() }()
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	}
}

func handleList(workdir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		encoded, ok := trimPrefixEscaped(r.URL.EscapedPath(), "/list/")
		if !ok {
			writeError(w, http.StatusNotFound, "missing path")
			return
		}
		path, err := resolveSafe(workdir, encoded)
		if err != nil {
			if errors.Is(err, errAccessDenied) {
				writeError(w, http.StatusForbidden, err.Error())
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "directory not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			fileType := "file"
			if info.IsDir() {
				fileType = "directory"
			}
			out = append(out, map[string]any{
				"name":     info.Name(),
				"size":     info.Size(),
				"type":     fileType,
				"mod_time": float64(info.ModTime().UnixNano()) / 1e9,
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func handleExists(workdir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		encoded, ok := trimPrefixEscaped(r.URL.EscapedPath(), "/exists/")
		if !ok {
			writeError(w, http.StatusNotFound, "missing path")
			return
		}
		path, err := resolveSafe(workdir, encoded)
		if err != nil {
			if errors.Is(err, errAccessDenied) {
				writeError(w, http.StatusForbidden, err.Error())
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		_, statErr := os.Stat(path)
		writeJSON(w, http.StatusOK, map[string]any{
			"path":   strings.TrimPrefix(path, workdir),
			"exists": statErr == nil,
		})
	}
}

func trimPrefixEscaped(escapedPath, prefix string) (string, bool) {
	if !strings.HasPrefix(escapedPath, prefix) {
		return "", false
	}
	return escapedPath[len(prefix):], true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"detail": message})
}
