package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"gitlab.com/sfoerster/butler/internal/config"
)

type Proxy struct {
	config  *config.Config
	reverse *httputil.ReverseProxy
	logger  *slog.Logger
}

func New(cfg *config.Config, logger *slog.Logger) (*Proxy, error) {
	upstream, err := url.Parse(cfg.Upstream)
	if err != nil {
		return nil, fmt.Errorf("parsing upstream URL: %w", err)
	}

	p := &Proxy{
		config: cfg,
		logger: logger,
	}

	p.reverse = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			r.Out.Host = upstream.Host
			r.Out.Header.Del("Authorization")
			r.Out.Header.Del("Proxy-Authorization")
		},
		FlushInterval: -1, // flush immediately for streaming
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error("proxy error", "error", err, "path", r.URL.Path)
			writeJSON(w, http.StatusBadGateway, `{"error":"upstream unavailable"}`)
		},
	}

	return p, nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Authenticate
	client := p.authenticate(r)
	if client == nil {
		p.logger.Warn("unauthorized request",
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
		)
		writeJSON(w, http.StatusUnauthorized, `{"error":"unauthorized"}`)
		return
	}

	// Extract model from request body (if applicable)
	model, body, err := extractModel(r)
	if err != nil {
		status := http.StatusBadRequest
		resp := `{"error":"bad request"}`
		if errors.Is(err, errBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
			resp = `{"error":"request body too large"}`
		}
		p.logger.Error("failed to read request body",
			"error", err,
			"client", client.Name,
		)
		writeJSON(w, status, resp)
		return
	}

	// Restore body for proxying
	if body != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
	}

	// ACL check
	if model != "" && !client.ModelAllowed(model) {
		p.logger.Warn("model denied",
			"client", client.Name,
			"model", model,
			"path", r.URL.Path,
		)
		writeJSON(w, http.StatusForbidden, `{"error":"model not allowed"}`)
		return
	}

	p.logger.Info("request",
		"client", client.Name,
		"model", model,
		"method", r.Method,
		"path", r.URL.Path,
		"remote", r.RemoteAddr,
	)

	// Wrap response writer to capture status code
	wrapped := &statusWriter{ResponseWriter: w}
	p.reverse.ServeHTTP(wrapped, r)

	p.logger.Info("response",
		"client", client.Name,
		"model", model,
		"path", r.URL.Path,
		"status", wrapped.code,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

func (p *Proxy) authenticate(r *http.Request) *config.Client {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, token, ok := strings.Cut(auth, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return nil
	}
	key := strings.TrimSpace(token)
	if key == "" {
		return nil
	}
	return p.config.ClientByKey(key)
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	code    int
	written bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.written {
		w.code = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.code = http.StatusOK
		w.written = true
	}
	return w.ResponseWriter.Write(b)
}

// Flush implements http.Flusher for streaming support.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter for http.ResponseController.
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func writeJSON(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}
