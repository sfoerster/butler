package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sfoerster/butler/internal/config"
)

func newTestProxy(t *testing.T, upstream *httptest.Server) *Proxy {
	t.Helper()
	cfg := &config.Config{
		Listen:   "127.0.0.1:0",
		Upstream: upstream.URL,
		Clients: []config.Client{
			{
				Name:        "allowed-client",
				Key:         "sk-allowed",
				AllowModels: []string{"llama3.2", "mistral"},
			},
			{
				Name:        "admin-client",
				Key:         "sk-admin",
				AllowModels: []string{"*"},
			},
			{
				Name:        "restricted-client",
				Key:         "sk-restricted",
				AllowModels: []string{"llama3.2:1b"},
			},
		},
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	p, err := New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNoAuth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be reached")
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/tags", nil)
	p.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestBadBearerToken(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be reached")
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/tags", nil)
	r.Header.Set("Authorization", "Bearer sk-wrong")
	p.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestAllowedModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"llama3.2","response":"hello"}`))
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)
	body := `{"model":"llama3.2","prompt":"hi"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/generate", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer sk-allowed")
	r.Header.Set("Content-Type", "application/json")
	p.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestDeniedModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be reached for denied model")
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)
	body := `{"model":"gpt-4","prompt":"hi"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/generate", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer sk-allowed")
	r.Header.Set("Content-Type", "application/json")
	p.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestWildcardAccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)
	body := `{"model":"any-model-at-all","prompt":"hi"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/generate", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer sk-admin")
	r.Header.Set("Content-Type", "application/json")
	p.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestNonModelEndpointPassesThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/tags", nil)
	r.Header.Set("Authorization", "Bearer sk-restricted")
	p.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestOpenAICompatEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the path is forwarded correctly
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("upstream path = %q, want /v1/chat/completions", r.URL.Path)
		}
		// Verify body is intact
		body, _ := io.ReadAll(r.Body)
		var payload map[string]interface{}
		_ = json.Unmarshal(body, &payload)
		if payload["model"] != "llama3.2" {
			t.Errorf("upstream model = %v, want llama3.2", payload["model"])
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello"}}]}`))
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)
	body := `{"model":"llama3.2","messages":[{"role":"user","content":"hi"}]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer sk-allowed")
	r.Header.Set("Content-Type", "application/json")
	p.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestStreamingPassthrough(t *testing.T) {
	chunks := []string{
		`{"model":"llama3.2","response":"Hello","done":false}`,
		`{"model":"llama3.2","response":" world","done":false}`,
		`{"model":"llama3.2","response":"","done":true}`,
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Transfer-Encoding", "chunked")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not support flushing")
		}
		for _, chunk := range chunks {
			_, _ = w.Write([]byte(chunk + "\n"))
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)
	body := `{"model":"llama3.2","prompt":"hi","stream":true}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/generate", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer sk-allowed")
	r.Header.Set("Content-Type", "application/json")
	p.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify all chunks were forwarded
	respBody := w.Body.String()
	for _, chunk := range chunks {
		if !strings.Contains(respBody, chunk) {
			t.Errorf("response missing chunk: %s", chunk)
		}
	}
}

func TestRequestBodyPreserved(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("upstream received invalid JSON: %v", err)
		}
		if payload["prompt"] != "tell me a joke" {
			t.Errorf("prompt = %v, want 'tell me a joke'", payload["prompt"])
		}
		_, _ = w.Write([]byte(`{"response":"ok"}`))
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)
	body := `{"model":"llama3.2","prompt":"tell me a joke"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/generate", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer sk-allowed")
	r.Header.Set("Content-Type", "application/json")
	p.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestErrorResponseFormat(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be reached")
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)

	tests := []struct {
		name       string
		auth       string
		path       string
		body       string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "unauthorized",
			auth:       "",
			path:       "/api/tags",
			wantStatus: http.StatusUnauthorized,
			wantBody:   `{"error":"unauthorized"}`,
		},
		{
			name:       "model denied",
			auth:       "Bearer sk-allowed",
			path:       "/api/generate",
			body:       `{"model":"gpt-4","prompt":"hi"}`,
			wantStatus: http.StatusForbidden,
			wantBody:   `{"error":"model not allowed"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var bodyReader io.Reader
			if tt.body != "" {
				bodyReader = strings.NewReader(tt.body)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", tt.path, bodyReader)
			if tt.auth != "" {
				r.Header.Set("Authorization", tt.auth)
			}
			p.ServeHTTP(w, r)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			if got := strings.TrimSpace(w.Body.String()); got != tt.wantBody {
				t.Errorf("body = %q, want %q", got, tt.wantBody)
			}
		})
	}
}

func TestNonBearerAuthRejected(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be reached")
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)

	schemes := []string{
		"Basic dXNlcjpwYXNz",
		"Token sk-allowed",
		"sk-allowed",
	}
	for _, auth := range schemes {
		t.Run(auth, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/api/tags", nil)
			r.Header.Set("Authorization", auth)
			p.ServeHTTP(w, r)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("Authorization %q: status = %d, want %d", auth, w.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestBearerAuthCaseInsensitive(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)

	for _, auth := range []string{"bearer sk-admin", "BEARER sk-admin", "BeArEr sk-admin"} {
		t.Run(auth, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/api/tags", nil)
			r.Header.Set("Authorization", auth)
			p.ServeHTTP(w, r)

			if w.Code != http.StatusOK {
				t.Errorf("Authorization %q: status = %d, want %d", auth, w.Code, http.StatusOK)
			}
		})
	}
}

func TestEmptyBearerTokenRejected(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be reached")
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/tags", nil)
	r.Header.Set("Authorization", "Bearer ")
	p.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestDenylistThroughProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be reached for denied model")
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Listen:   "127.0.0.1:0",
		Upstream: upstream.URL,
		Clients: []config.Client{
			{
				Name:        "mixed-client",
				Key:         "sk-mixed",
				AllowModels: []string{"*"},
				DenyModels:  []string{"forbidden-model"},
			},
		},
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	p, err := New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"model":"forbidden-model","prompt":"hi"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/generate", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer sk-mixed")
	p.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}

	// Same client can use other models
	upstream2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream2.Close()

	p2, _ := New(&config.Config{
		Listen:   "127.0.0.1:0",
		Upstream: upstream2.URL,
		Clients:  cfg.Clients,
	}, logger)

	body2 := `{"model":"llama3.2","prompt":"hi"}`
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("POST", "/api/generate", strings.NewReader(body2))
	r2.Header.Set("Authorization", "Bearer sk-mixed")
	p2.ServeHTTP(w2, r2)

	if w2.Code != http.StatusOK {
		t.Errorf("allowed model: status = %d, want %d", w2.Code, http.StatusOK)
	}
}

func TestManagementEndpointACL(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)

	// restricted-client can only use llama3.2:1b — /api/show for llama3.2:70b should be denied
	body := `{"name":"llama3.2:70b"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/show", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer sk-restricted")
	p.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("denied show: status = %d, want %d", w.Code, http.StatusForbidden)
	}

	// Same client can show allowed model
	body2 := `{"name":"llama3.2:1b"}`
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("POST", "/api/show", strings.NewReader(body2))
	r2.Header.Set("Authorization", "Bearer sk-restricted")
	p.ServeHTTP(w2, r2)

	if w2.Code != http.StatusOK {
		t.Errorf("allowed show: status = %d, want %d", w2.Code, http.StatusOK)
	}
}

func TestUpstreamErrorForwarded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"model 'nonexistent' not found"}`))
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)
	body := `{"model":"llama3.2","prompt":"hi"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/generate", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer sk-allowed")
	p.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if !strings.Contains(w.Body.String(), "not found") {
		t.Errorf("body = %q, want upstream error body", w.Body.String())
	}
}

func TestAuthHeaderStrippedFromUpstream(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/tags", nil)
	r.Header.Set("Authorization", "Bearer sk-admin")
	p.ServeHTTP(w, r)

	if gotAuth != "" {
		t.Errorf("upstream Authorization = %q, want empty header", gotAuth)
	}
}

func TestModelEndpointRejectsOversizedBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be reached for oversized body")
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)
	body := `{"model":"llama3.2","prompt":"` + strings.Repeat("a", maxModelInspectBodyBytes) + `"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/generate", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer sk-allowed")
	r.Header.Set("Content-Type", "application/json")
	p.ServeHTTP(w, r)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if got := strings.TrimSpace(w.Body.String()); got != `{"error":"request body too large"}` {
		t.Errorf("body = %q, want request-body-too-large error", got)
	}
}

func TestExtractModel(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		body      string
		wantModel string
	}{
		{"chat model", "/api/chat", `{"model":"llama3.2","messages":[]}`, "llama3.2"},
		{"generate model", "/api/generate", `{"model":"mistral","prompt":"hi"}`, "mistral"},
		{"openai chat", "/v1/chat/completions", `{"model":"llama3.2"}`, "llama3.2"},
		{"show name", "/api/show", `{"name":"llama3.2"}`, "llama3.2"},
		{"non-model path", "/api/tags", ``, ""},
		{"empty body", "/api/chat", ``, ""},
		{"invalid json", "/api/chat", `not json`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.body != "" {
				body = bytes.NewReader([]byte(tt.body))
			}
			r := httptest.NewRequest("POST", tt.path, body)
			model, _, err := extractModel(r)
			if err != nil {
				t.Fatal(err)
			}
			if model != tt.wantModel {
				t.Errorf("model = %q, want %q", model, tt.wantModel)
			}
		})
	}
}
