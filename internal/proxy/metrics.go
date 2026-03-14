package proxy

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const numBuckets = 10

var durationBuckets = [numBuckets]float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

type histogram struct {
	buckets [numBuckets]int64
	count   int64
	sum     float64
}

func (h *histogram) observe(v float64) {
	h.count++
	h.sum += v
	for i, b := range durationBuckets {
		if v <= b {
			h.buckets[i]++
			return
		}
	}
	// value exceeds all buckets — only counted in +Inf (via h.count)
}

type metrics struct {
	mu               sync.Mutex
	requestsTotal    map[string]int64
	requestsRejected map[string]int64
	durations        map[string]*histogram
}

func newMetrics() *metrics {
	return &metrics{
		requestsTotal:    make(map[string]int64),
		requestsRejected: make(map[string]int64),
		durations:        make(map[string]*histogram),
	}
}

// RecordRequest records a completed request.
func (m *metrics) RecordRequest(client, model, path string, status int, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	counterKey := fmt.Sprintf("client=%q,model=%q,path=%q,status=%q", client, model, path, fmt.Sprintf("%d", status))
	m.requestsTotal[counterKey]++

	histKey := fmt.Sprintf("client=%q,model=%q,path=%q", client, model, path)
	h := m.durations[histKey]
	if h == nil {
		h = &histogram{}
		m.durations[histKey] = h
	}
	h.observe(duration.Seconds())
}

// RecordRejection records a rejected request.
func (m *metrics) RecordRejection(client, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("client=%q,reason=%q", client, reason)
	m.requestsRejected[key]++
}

// Handler returns an http.Handler that serves /metrics in Prometheus text exposition format.
func (m *metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()

		var b strings.Builder

		// butler_requests_total
		b.WriteString("# HELP butler_requests_total Total number of proxied requests.\n")
		b.WriteString("# TYPE butler_requests_total counter\n")
		for _, key := range sortedKeys(m.requestsTotal) {
			fmt.Fprintf(&b, "butler_requests_total{%s} %d\n", key, m.requestsTotal[key])
		}

		// butler_requests_rejected_total
		b.WriteString("# HELP butler_requests_rejected_total Total number of rejected requests.\n")
		b.WriteString("# TYPE butler_requests_rejected_total counter\n")
		for _, key := range sortedKeys(m.requestsRejected) {
			fmt.Fprintf(&b, "butler_requests_rejected_total{%s} %d\n", key, m.requestsRejected[key])
		}

		// butler_request_duration_seconds
		b.WriteString("# HELP butler_request_duration_seconds Request duration in seconds.\n")
		b.WriteString("# TYPE butler_request_duration_seconds histogram\n")
		for _, key := range sortedKeys(m.durations) {
			h := m.durations[key]
			var cumulative int64
			for i, bucket := range durationBuckets {
				cumulative += h.buckets[i]
				fmt.Fprintf(&b, "butler_request_duration_seconds_bucket{%s,le=\"%s\"} %d\n",
					key, formatFloat(bucket), cumulative)
			}
			fmt.Fprintf(&b, "butler_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", key, h.count)
			fmt.Fprintf(&b, "butler_request_duration_seconds_sum{%s} %s\n", key, formatFloat(h.sum))
			fmt.Fprintf(&b, "butler_request_duration_seconds_count{%s} %d\n", key, h.count)
		}

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(b.String()))
	})
}

func formatFloat(f float64) string {
	s := fmt.Sprintf("%g", f)
	if !strings.Contains(s, ".") && !strings.Contains(s, "e") && !strings.Contains(s, "E") {
		s += ".0"
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
