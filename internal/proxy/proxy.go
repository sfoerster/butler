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

	"github.com/sfoerster/butler/internal/auth"
	"github.com/sfoerster/butler/internal/config"
)

type Proxy struct {
	config  *config.Config
	reverse *httputil.ReverseProxy
	logger  *slog.Logger
	limiter *rateLimiter
	metrics *metrics
	jwt     *auth.JWTService
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

	if cfg.Auth.Mode == "jwt_standalone" || cfg.Auth.Mode == "either" {
		p.jwt = auth.NewJWTService(cfg.Auth.JWTSecret, cfg.Auth.TokenExpiryDuration())
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
	case "/auth/login":
		p.handleLogin(w, r)
		return
	}

	// 1. Authenticate
	subj := p.authenticate(r)
	if subj == nil {
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
			"client", subj.Name,
			"path", r.URL.Path,
		)
		p.metrics.RecordRejection(subj.Name, "rate_limited")
		writeJSON(w, http.StatusTooManyRequests, `{"error":"rate limit exceeded"}`)
		return
	}

	// 3. Per-subject rate limit
	if !p.limiter.Allow(subj.RateLimitKey(), subj.Rate) {
		p.logger.Warn("client rate limit exceeded",
			"client", subj.Name,
			"path", r.URL.Path,
		)
		p.metrics.RecordRejection(subj.Name, "rate_limited")
		writeJSON(w, http.StatusTooManyRequests, `{"error":"rate limit exceeded"}`)
		return
	}

	// 4. Body inspection
	info, err := inspectRequest(r, subj.MaxReqBytes)
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
			"client", subj.Name,
		)
		p.metrics.RecordRejection(subj.Name, reason)
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
	if model != "" && !subj.ModelAllowed(model) {
		p.logger.Warn("model denied",
			"client", subj.Name,
			"model", model,
			"path", r.URL.Path,
		)
		p.metrics.RecordRejection(subj.Name, "model_denied")
		writeJSON(w, http.StatusForbidden, `{"error":"model not allowed"}`)
		return
	}

	// 7. num_ctx cap
	if info != nil && subj.MaxCtx > 0 && info.NumCtx > subj.MaxCtx {
		p.logger.Warn("num_ctx exceeds limit",
			"client", subj.Name,
			"num_ctx", info.NumCtx,
			"limit", subj.MaxCtx,
		)
		p.metrics.RecordRejection(subj.Name, "num_ctx_exceeded")
		writeJSON(w, http.StatusBadRequest,
			fmt.Sprintf(`{"error":"num_ctx %d exceeds limit of %d"}`, info.NumCtx, subj.MaxCtx))
		return
	}

	// 8. num_predict cap
	if info != nil && subj.MaxPredict > 0 && info.NumPredict > subj.MaxPredict {
		p.logger.Warn("num_predict exceeds limit",
			"client", subj.Name,
			"num_predict", info.NumPredict,
			"limit", subj.MaxPredict,
		)
		p.metrics.RecordRejection(subj.Name, "num_predict_exceeded")
		writeJSON(w, http.StatusBadRequest,
			fmt.Sprintf(`{"error":"num_predict %d exceeds limit of %d"}`, info.NumPredict, subj.MaxPredict))
		return
	}

	// 9. Prompt pattern rejection
	if info != nil && len(subj.DenyPatterns) > 0 {
		for _, re := range subj.DenyPatterns {
			for _, prompt := range info.Prompts {
				if re.MatchString(prompt) {
					p.logger.Warn("prompt rejected",
						"client", subj.Name,
						"pattern", re.String(),
						"path", r.URL.Path,
					)
					p.metrics.RecordRejection(subj.Name, "prompt_rejected")
					writeJSON(w, http.StatusForbidden, `{"error":"prompt rejected"}`)
					return
				}
			}
		}
	}

	// 10. Log + proxy + response log
	logAttrs := []any{
		"client", subj.Name,
		"model", model,
		"method", r.Method,
		"path", r.URL.Path,
		"remote", r.RemoteAddr,
	}
	if subj.AuthSource == "jwt" {
		logAttrs = append(logAttrs, "user", subj.Name)
	}
	p.logger.Info("request", logAttrs...)

	// Optional prompt logging
	if p.config.LogPrompts && info != nil && len(info.Prompts) > 0 {
		p.logger.Info("prompts",
			"client", subj.Name,
			"model", model,
			"path", r.URL.Path,
			"prompts", info.Prompts,
		)
	}

	wrapped := &statusWriter{ResponseWriter: w}
	p.reverse.ServeHTTP(wrapped, r)

	duration := time.Since(start)
	p.logger.Info("response",
		"client", subj.Name,
		"model", model,
		"path", r.URL.Path,
		"status", wrapped.code,
		"duration_ms", duration.Milliseconds(),
	)

	p.metrics.RecordRequest(subj.Name, model, r.URL.Path, wrapped.code, duration)
}

func (p *Proxy) authenticate(r *http.Request) *config.Subject {
	hdr := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, token, ok := strings.Cut(hdr, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return nil
	}
	key := strings.TrimSpace(token)
	if key == "" {
		return nil
	}

	mode := p.config.Auth.Mode
	if mode == "" {
		mode = "api_key"
	}

	// Try API key lookup
	if mode == "api_key" || mode == "either" {
		if client := p.config.ClientByKey(key); client != nil {
			return client.Subject()
		}
		if mode == "api_key" {
			return nil
		}
	}

	// Try JWT validation
	if mode == "jwt_standalone" || mode == "either" {
		if p.jwt != nil {
			username, err := p.jwt.Validate(key)
			if err != nil {
				return nil
			}
			user := p.config.UserByName(username)
			if user == nil {
				return nil
			}
			return user.Subject()
		}
	}

	return nil
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
