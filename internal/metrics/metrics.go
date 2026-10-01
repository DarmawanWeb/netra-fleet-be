// Package metrics is a minimal Prometheus registry (counters and gauges with labels).
package metrics

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type series struct {
	help, kind string
	values     map[string]float64
}

// Registry holds every metric.
type Registry struct {
	mu      sync.Mutex
	metrics map[string]*series
}

// Default is the process-wide registry.
var Default = &Registry{metrics: map[string]*series{}}

func labelKey(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	parts := make([]string, 0, len(labels)/2)
	for i := 0; i+1 < len(labels); i += 2 {
		parts = append(parts, fmt.Sprintf(`%s=%q`, labels[i], labels[i+1]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func (r *Registry) get(name, help, kind string) *series {
	m, ok := r.metrics[name]
	if !ok {
		m = &series{help: help, kind: kind, values: map[string]float64{}}
		r.metrics[name] = m
	}
	return m
}

// Add increments a counter; labels are key, value pairs.
func (r *Registry) Add(name, help string, v float64, labels ...string) {
	r.mu.Lock()
	r.get(name, help, "counter").values[labelKey(labels)] += v
	r.mu.Unlock()
}

// Set sets a gauge.
func (r *Registry) Set(name, help string, v float64, labels ...string) {
	r.mu.Lock()
	r.get(name, help, "gauge").values[labelKey(labels)] = v
	r.mu.Unlock()
}

// ServeHTTP writes the Prometheus text exposition format.
func (r *Registry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	names := make([]string, 0, len(r.metrics))
	for n := range r.metrics {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		m := r.metrics[n]
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", n, m.help, n, m.kind)
		keys := make([]string, 0, len(m.values))
		for k := range m.values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "%s%s %g\n", n, k, m.values[k])
		}
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) { s.status = code; s.ResponseWriter.WriteHeader(code) }

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Hijack passes WebSocket upgrades through.
func (s *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack not supported")
	}
	return h.Hijack()
}

// Flush passes streaming responses through.
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Middleware counts requests and latency per mux route pattern and status class.
func Middleware(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		_, route := mux.Handler(r)
		if route == "" {
			route = "unmatched"
		}
		class := fmt.Sprintf("%dxx", sw.status/100)
		Default.Add("netra_http_requests_total", "HTTP requests.", 1, "route", route, "code", class)
		Default.Add("netra_http_request_seconds_sum", "Total HTTP request time.", time.Since(start).Seconds(), "route", route)
	})
}
