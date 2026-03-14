package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sfoerster/butler/internal/config"
)

func TestHandleHealthzHealthy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			t.Errorf("health check path = %q, want /api/version", r.URL.Path)
		}
		if r.Method != http.MethodHead {
			t.Errorf("health check method = %q, want HEAD", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Listen:   "127.0.0.1:0",
		Upstream: upstream.URL,
		Clients:  []config.Client{{Name: "test", Key: "sk-test", AllowModels: []string{"*"}}},
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	p, err := New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/healthz", nil)
	p.handleHealthz(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Errorf("body = %q, want ok status", w.Body.String())
	}
}

func TestHandleHealthzUnhealthy(t *testing.T) {
	// Point at a closed server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	upstreamURL := upstream.URL
	upstream.Close()

	cfg := &config.Config{
		Listen:   "127.0.0.1:0",
		Upstream: upstreamURL,
		Clients:  []config.Client{{Name: "test", Key: "sk-test", AllowModels: []string{"*"}}},
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	p, err := New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/healthz", nil)
	p.handleHealthz(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(w.Body.String(), `"status":"unhealthy"`) {
		t.Errorf("body = %q, want unhealthy status", w.Body.String())
	}
}

func TestHandleHealthzUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Listen:   "127.0.0.1:0",
		Upstream: upstream.URL,
		Clients:  []config.Client{{Name: "test", Key: "sk-test", AllowModels: []string{"*"}}},
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	p, err := New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/healthz", nil)
	p.handleHealthz(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(w.Body.String(), "500") {
		t.Errorf("body = %q, want upstream status code in error", w.Body.String())
	}
}
