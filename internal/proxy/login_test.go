package proxy

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sfoerster/butler/internal/config"
	"golang.org/x/crypto/bcrypt"
)

const jwtTestSecret = "this-is-a-very-long-secret-key-for-testing-purposes"

func hashPassword(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt error: %v", err)
	}
	return string(hash)
}

func newJWTProxy(t *testing.T, upstream *httptest.Server) *Proxy {
	t.Helper()
	cfg := &config.Config{
		Listen:   "127.0.0.1:0",
		Upstream: upstream.URL,
		Auth: config.AuthConfig{
			Mode:      "jwt_standalone",
			JWTSecret: jwtTestSecret,
		},
		Users: []config.User{
			{
				Name:         "alice",
				PasswordHash: hashPassword(t, "hunter2"),
				AllowModels:  []string{"llama3.2", "mistral"},
			},
			{
				Name:         "bob",
				PasswordHash: hashPassword(t, "secret123"),
				AllowModels:  []string{"*"},
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

func TestLoginSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	p := newJWTProxy(t, upstream)
	body := `{"username":"alice","password":"hunter2"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/auth/login", strings.NewReader(body))
	p.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp loginResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Token == "" {
		t.Error("token is empty")
	}
}

func TestLoginWrongPassword(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	p := newJWTProxy(t, upstream)
	body := `{"username":"alice","password":"wrong"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/auth/login", strings.NewReader(body))
	p.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if !strings.Contains(w.Body.String(), "invalid credentials") {
		t.Errorf("body = %q, want invalid credentials", w.Body.String())
	}
}

func TestLoginUnknownUser(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	p := newJWTProxy(t, upstream)
	body := `{"username":"charlie","password":"anything"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/auth/login", strings.NewReader(body))
	p.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	// Same error as wrong password — no info leakage
	if !strings.Contains(w.Body.String(), "invalid credentials") {
		t.Errorf("body = %q, want invalid credentials", w.Body.String())
	}
}

func TestLoginMissingFields(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	p := newJWTProxy(t, upstream)

	tests := []struct {
		name string
		body string
	}{
		{"empty username", `{"username":"","password":"hunter2"}`},
		{"empty password", `{"username":"alice","password":""}`},
		{"both empty", `{"username":"","password":""}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/auth/login", strings.NewReader(tt.body))
			p.ServeHTTP(w, r)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestLoginMethodNotAllowed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	p := newJWTProxy(t, upstream)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/auth/login", nil)
	p.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestLoginNotAvailableInAPIKeyMode(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	p := newTestProxy(t, upstream)
	body := `{"username":"alice","password":"hunter2"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/auth/login", strings.NewReader(body))
	p.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestLoginTokenWorksForRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	p := newJWTProxy(t, upstream)

	// Login
	loginBody := `{"username":"alice","password":"hunter2"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/auth/login", strings.NewReader(loginBody))
	p.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp loginResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	// Use token for a request
	reqBody := `{"model":"llama3.2","prompt":"hi"}`
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("POST", "/api/generate", strings.NewReader(reqBody))
	r2.Header.Set("Authorization", "Bearer "+resp.Token)
	p.ServeHTTP(w2, r2)

	if w2.Code != http.StatusOK {
		t.Errorf("request status = %d, want %d, body = %s", w2.Code, http.StatusOK, w2.Body.String())
	}
}
