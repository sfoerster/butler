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

	"github.com/sfoerster/butler/internal/config"
)

type Proxy struct {
	config  *config.Config
	reverse *httputil.ReverseProxy
	logger  *slog.Logger
	limiter *rateLimiter
	metrics *metrics
}

func New(cfg *config.Config, logger *slog.Logger) (*Proxy, error) {
	upstream, err := url.Parse(cfg.Upstream)
	if err != nil {
		return nil, fmt.Errorf("parsing upstream URL: %w", err)
	}

	p := &Proxy{
		config:  cfg,
		logger:  logger,
		limiter: newRateLimiter(),
		metrics: newMetrics(),
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

	// Unauthenticated endpoints
	switch r.URL.Path {
	case "/healthz":
		p.handleHealthz(w, r)
		return
	case "/metrics":
		p.metrics.Handler().ServeHTTP(w, r)
		return
	}

	// 1. Authenticate
	client := p.authenticate(r)
	if client == nil {
		p.logger.Warn("unauthorized request",
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
		)
		p.metrics.RecordRejection("", "unauthorized")
		writeJSON(w, http.StatusUnauthorized, `{"error":"unauthorized"}`)
		return
	}

	// 2. Global rate limit
	if !p.limiter.Allow("__global__", p.config.GlobalRate()) {
		p.logger.Warn("global rate limit exceeded",
			"client", client.Name,
			"path", r.URL.Path,
		)
		p.metrics.RecordRejection(client.Name, "rate_limited")
		writeJSON(w, http.StatusTooManyRequests, `{"error":"rate limit exceeded"}`)
		return
	}

	// 3. Per-client rate limit
	if !p.limiter.Allow(client.Name, client.Rate()) {
		p.logger.Warn("client rate limit exceeded",
			"client", client.Name,
			"path", r.URL.Path,
		)
		p.metrics.RecordRejection(client.Name, "rate_limited")
		writeJSON(w, http.StatusTooManyRequests, `{"error":"rate limit exceeded"}`)
		return
	}

	// 4. Body inspection
	info, err := inspectRequest(r, client.MaxRequestBytes)
	if err != nil {
		status := http.StatusBadRequest
		resp := `{"error":"bad request"}`
		reason := "too_large"
		if errors.Is(err, errBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
			resp = `{"error":"request body too large"}`
		}
		if errors.Is(err, errRequestTooLarge) {
			status = http.StatusRequestEntityTooLarge
			resp = `{"error":"request too large"}`
		}
		p.logger.Error("failed to read request body",
			"error", err,
			"client", client.Name,
		)
		p.metrics.RecordRejection(client.Name, reason)
		writeJSON(w, status, resp)
		return
	}

	var model string
	if info != nil {
		model = info.Model

		// 5. Restore body for proxying
		r.Body = io.NopCloser(bytes.NewReader(info.Body))
		r.ContentLength = int64(len(info.Body))
	}

	// 6. Model ACL
	if model != "" && !client.ModelAllowed(model) {
		p.logger.Warn("model denied",
			"client", client.Name,
			"model", model,
			"path", r.URL.Path,
		)
		p.metrics.RecordRejection(client.Name, "model_denied")
		writeJSON(w, http.StatusForbidden, `{"error":"model not allowed"}`)
		return
	}

	// 7. num_ctx cap
	if info != nil && client.MaxCtx > 0 && info.NumCtx > client.MaxCtx {
		p.logger.Warn("num_ctx exceeds limit",
			"client", client.Name,
			"num_ctx", info.NumCtx,
			"limit", client.MaxCtx,
		)
		p.metrics.RecordRejection(client.Name, "num_ctx_exceeded")
		writeJSON(w, http.StatusBadRequest,
			fmt.Sprintf(`{"error":"num_ctx %d exceeds limit of %d"}`, info.NumCtx, client.MaxCtx))
		return
	}

	// 8. num_predict cap
	if info != nil && client.MaxPredict > 0 && info.NumPredict > client.MaxPredict {
		p.logger.Warn("num_predict exceeds limit",
			"client", client.Name,
			"num_predict", info.NumPredict,
			"limit", client.MaxPredict,
		)
		p.metrics.RecordRejection(client.Name, "num_predict_exceeded")
		writeJSON(w, http.StatusBadRequest,
			fmt.Sprintf(`{"error":"num_predict %d exceeds limit of %d"}`, info.NumPredict, client.MaxPredict))
		return
	}

	// 9. Prompt pattern rejection
	if info != nil && len(client.DenyPatterns()) > 0 {
		for _, re := range client.DenyPatterns() {
			for _, prompt := range info.Prompts {
				if re.MatchString(prompt) {
					p.logger.Warn("prompt rejected",
						"client", client.Name,
						"pattern", re.String(),
						"path", r.URL.Path,
					)
					p.metrics.RecordRejection(client.Name, "prompt_rejected")
					writeJSON(w, http.StatusForbidden, `{"error":"prompt rejected"}`)
					return
				}
			}
		}
	}

	// 10. Log + proxy + response log
	p.logger.Info("request",
		"client", client.Name,
		"model", model,
		"method", r.Method,
		"path", r.URL.Path,
		"remote", r.RemoteAddr,
	)

	// Optional prompt logging
	if p.config.LogPrompts && info != nil && len(info.Prompts) > 0 {
		p.logger.Info("prompts",
			"client", client.Name,
			"model", model,
			"path", r.URL.Path,
			"prompts", info.Prompts,
		)
	}

	wrapped := &statusWriter{ResponseWriter: w}
	p.reverse.ServeHTTP(wrapped, r)

	duration := time.Since(start)
	p.logger.Info("response",
		"client", client.Name,
		"model", model,
		"path", r.URL.Path,
		"status", wrapped.code,
		"duration_ms", duration.Milliseconds(),
	)

	p.metrics.RecordRequest(client.Name, model, r.URL.Path, wrapped.code, duration)
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
