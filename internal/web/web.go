// Package web serves the dashboard: the static export of netra-fleet-fe, built into the binary
// (build tag webui, files in internal/web/dist) or read from a directory (NETRA_WEB_DIR).
package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

// embedded holds the dashboard built into the binary; nil without the webui build tag.
var embedded fs.FS

// Files returns the dashboard: dir when given, else the built-in one. ok is false when there is
// none (the dashboard is then served by another container).
func Files(dir string) (files fs.FS, ok bool, err error) {
	if dir != "" {
		info, err := os.Stat(dir)
		if err != nil {
			return nil, false, err
		}
		if !info.IsDir() {
			return nil, false, errors.New(dir + " is not a directory")
		}
		return os.DirFS(dir), true, nil
	}
	if embedded == nil {
		return nil, false, nil
	}
	if _, err := fs.Stat(embedded, "index.html"); err != nil {
		return nil, false, nil // built with webui but without the export
	}
	return embedded, true, nil
}

// detail matches the detail routes the export holds once (/anomalies/0.html) for every id.
var detail = regexp.MustCompile(`^/(anomalies|patrols|robots|substation/(?:assets|points|findings))/[^/]+?(\.txt)?$`)

// Resolve maps a request path to the file that answers it: the path, path.html,
// path/index.html, then the detail page of its section. ok is false for a 404.
func Resolve(files fs.FS, urlPath string) (string, bool) {
	clean := path.Clean("/" + urlPath)
	rel := strings.TrimPrefix(clean, "/")
	candidates := []string{rel, rel + ".html", path.Join(rel, "index.html")}
	if rel == "" {
		candidates = []string{"index.html"}
	}
	if m := detail.FindStringSubmatch(clean); m != nil {
		ext := ".html"
		if m[2] != "" {
			ext = ".txt"
		}
		candidates = append(candidates, m[1]+"/0"+ext)
	}
	for _, c := range candidates {
		if c == "" || c == "." {
			continue
		}
		if info, err := fs.Stat(files, c); err == nil && !info.IsDir() {
			return c, true
		}
	}
	return "", false
}

// Handler serves the dashboard files with long caching for the hashed assets, revalidation for
// the pages, and security headers.
func Handler(files fs.FS) http.Handler {
	h := &handler{files: files, tags: map[string]string{}}
	return h
}

type handler struct {
	files fs.FS
	mu    sync.Mutex
	tags  map[string]string // ETag per file, computed once
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := path.Clean("/" + r.URL.Path)
	if strings.HasPrefix(p, "/api/") || p == "/api" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"not found"}`+"\n")
		return
	}
	if p == "/" {
		target := "/dashboard"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	name, ok := Resolve(h.files, p)
	status := http.StatusOK
	if !ok {
		name, status = "404.html", http.StatusNotFound
	}
	data, err := fs.ReadFile(h.files, name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	header := w.Header()
	setSecurity(header, r)
	header.Set("Content-Type", contentType(name))
	if strings.HasPrefix(p, "/_next/static/") {
		header.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		header.Set("Cache-Control", "no-cache")
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
		_, _ = w.Write(data)
		return
	}
	header.Set("ETag", h.etag(name, data))
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

func (h *handler) etag(name string, data []byte) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if tag, ok := h.tags[name]; ok {
		return tag
	}
	sum := sha256.Sum256(data)
	tag := `"` + hex.EncodeToString(sum[:8]) + `"`
	h.tags[name] = tag
	return tag
}

func contentType(name string) string {
	switch path.Ext(name) {
	case ".txt":
		return "text/x-component" // the client-navigation payload of a page
	case ".mjs", ".js":
		return "application/javascript"
	}
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// setSecurity sets the headers of a dashboard answer. Next.js needs inline scripts; images come
// from the API, data URLs and map tiles; the live feed is a WebSocket to the same host.
func setSecurity(h http.Header, r *http.Request) {
	host := r.Host
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'self'",
		"script-src 'self' 'unsafe-inline'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob: https: http:",
		"font-src 'self' data:",
		"worker-src 'self' blob:",
		"connect-src 'self' ws://" + host + " wss://" + host,
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}, "; "))
}
