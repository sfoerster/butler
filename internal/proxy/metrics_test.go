package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsRecordRequest(t *testing.T) {
	m := newMetrics()
	m.RecordRequest("my-app", "llama3.2", "/api/chat", 200, 150*time.Millisecond)
	m.RecordRequest("my-app", "llama3.2", "/api/chat", 200, 250*time.Millisecond)
	m.RecordRequest("my-app", "mistral", "/api/generate", 500, 5*time.Second)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(w, r)

	body := w.Body.String()

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; version=0.0.4; charset=utf-8" {
		t.Errorf("Content-Type = %q, want Prometheus text format", ct)
	}

	// Check counter
	if !strings.Contains(body, `butler_requests_total{client="my-app",model="llama3.2",path="/api/chat",status="200"} 2`) {
		t.Errorf("missing expected counter line, body:\n%s", body)
	}
	if !strings.Contains(body, `butler_requests_total{client="my-app",model="mistral",path="/api/generate",status="500"} 1`) {
		t.Errorf("missing expected counter line for mistral, body:\n%s", body)
	}

	// Check HELP and TYPE lines
	if !strings.Contains(body, "# HELP butler_requests_total") {
		t.Error("missing HELP for butler_requests_total")
	}
	if !strings.Contains(body, "# TYPE butler_requests_total counter") {
		t.Error("missing TYPE for butler_requests_total")
	}

	// Check histogram
	if !strings.Contains(body, "# TYPE butler_request_duration_seconds histogram") {
		t.Error("missing TYPE for butler_request_duration_seconds")
	}
	if !strings.Contains(body, `butler_request_duration_seconds_bucket{client="my-app",model="llama3.2",path="/api/chat",le="0.25"} 2`) {
		t.Errorf("missing expected histogram bucket, body:\n%s", body)
	}
	if !strings.Contains(body, `butler_request_duration_seconds_count{client="my-app",model="llama3.2",path="/api/chat"} 2`) {
		t.Errorf("missing expected histogram count, body:\n%s", body)
	}
	if !strings.Contains(body, `butler_request_duration_seconds_bucket{client="my-app",model="llama3.2",path="/api/chat",le="+Inf"} 2`) {
		t.Errorf("missing +Inf bucket, body:\n%s", body)
	}
}

func TestMetricsRecordRejection(t *testing.T) {
	m := newMetrics()
	m.RecordRejection("my-app", "rate_limited")
	m.RecordRejection("my-app", "rate_limited")
	m.RecordRejection("other", "unauthorized")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(w, r)

	body := w.Body.String()

	if !strings.Contains(body, `butler_requests_rejected_total{client="my-app",reason="rate_limited"} 2`) {
		t.Errorf("missing expected rejection line, body:\n%s", body)
	}
	if !strings.Contains(body, `butler_requests_rejected_total{client="other",reason="unauthorized"} 1`) {
		t.Errorf("missing expected rejection line for other, body:\n%s", body)
	}
	if !strings.Contains(body, "# HELP butler_requests_rejected_total") {
		t.Error("missing HELP for butler_requests_rejected_total")
	}
	if !strings.Contains(body, "# TYPE butler_requests_rejected_total counter") {
		t.Error("missing TYPE for butler_requests_rejected_total")
	}
}

func TestMetricsEmptyOutput(t *testing.T) {
	m := newMetrics()

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(w, r)

	body := w.Body.String()

	// Should still have HELP/TYPE lines even with no data
	if !strings.Contains(body, "# HELP butler_requests_total") {
		t.Error("missing HELP even with no data")
	}
	if !strings.Contains(body, "# TYPE butler_request_duration_seconds histogram") {
		t.Error("missing histogram TYPE even with no data")
	}
}

func TestMetricsHistogramBuckets(t *testing.T) {
	m := newMetrics()
	// Record a very fast request (fits in 0.01 bucket)
	m.RecordRequest("c", "m", "/p", 200, 5*time.Millisecond)
	// Record a slow request (fits only in 30 and +Inf)
	m.RecordRequest("c", "m", "/p", 200, 25*time.Second)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(w, r)

	body := w.Body.String()

	// 0.01 bucket should have 1 (only the fast request)
	if !strings.Contains(body, `le="0.01"} 1`) {
		t.Errorf("expected 1 in 0.01 bucket, body:\n%s", body)
	}
	// 30 bucket should have 2 (both requests)
	if !strings.Contains(body, `le="30.0"} 2`) {
		t.Errorf("expected 2 in 30 bucket, body:\n%s", body)
	}
	// +Inf should have 2
	if !strings.Contains(body, `le="+Inf"} 2`) {
		t.Errorf("expected 2 in +Inf bucket, body:\n%s", body)
	}
	// count should be 2
	if !strings.Contains(body, `_count{client="c",model="m",path="/p"} 2`) {
		t.Errorf("expected count=2, body:\n%s", body)
	}
}
