package proxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

var errBodyTooLarge = errors.New("request body too large")
var errRequestTooLarge = errors.New("request too large")

const maxModelInspectBodyBytes = 8 << 20 // 8 MiB

// modelBearingPaths are URL paths that carry a model name in the request body.
var modelBearingPaths = map[string]bool{
	// Inference endpoints (model field)
	"/api/chat":            true,
	"/api/generate":        true,
	"/api/embeddings":      true,
	"/api/embed":           true,
	"/v1/chat/completions": true,
	"/v1/completions":      true,
	"/v1/embeddings":       true,
	// Management endpoints (name field)
	"/api/show":   true,
	"/api/pull":   true,
	"/api/push":   true,
	"/api/delete": true,
	"/api/create": true,
}

// RequestInfo holds extracted fields from a request body.
type RequestInfo struct {
	Model      string
	Body       []byte
	NumCtx     int
	NumPredict int
	Prompts    []string
}

// inspectRequest reads and parses the request body for model-bearing endpoints.
// Returns the extracted info, or (nil, nil) for non-model-bearing paths.
// If maxRequestBytes > 0, enforces a request size limit.
func inspectRequest(r *http.Request, maxRequestBytes int64) (*RequestInfo, error) {
	if !modelBearingPaths[r.URL.Path] {
		return nil, nil
	}
	if r.Body == nil {
		return nil, nil
	}

	// Check Content-Length fast path for configured max request size
	if maxRequestBytes > 0 && r.ContentLength > maxRequestBytes {
		return nil, errRequestTooLarge
	}

	limit := int64(maxModelInspectBodyBytes + 1)
	if maxRequestBytes > 0 && maxRequestBytes < int64(maxModelInspectBodyBytes) {
		limit = maxRequestBytes + 1
	}

	limited := io.LimitReader(r.Body, limit)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	_ = r.Body.Close()

	// Check configured max request size (actual byte count)
	if maxRequestBytes > 0 && int64(len(body)) > maxRequestBytes {
		return nil, errRequestTooLarge
	}

	// Check built-in inspection limit
	if len(body) > maxModelInspectBodyBytes {
		return nil, errBodyTooLarge
	}

	var payload struct {
		Model   string `json:"model"`
		Name    string `json:"name"`
		Prompt  string `json:"prompt"`
		NumCtx  int    `json:"num_ctx"`
		NumPredict int `json:"num_predict"`
		Options struct {
			NumCtx     int `json:"num_ctx"`
			NumPredict int `json:"num_predict"`
		} `json:"options"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		// Non-JSON body — let upstream handle it
		return &RequestInfo{Body: body}, nil
	}

	info := &RequestInfo{Body: body}

	// Model / name
	info.Model = payload.Model
	if info.Model == "" {
		info.Model = payload.Name
	}

	// num_ctx: top-level takes precedence over options
	info.NumCtx = payload.NumCtx
	if info.NumCtx == 0 {
		info.NumCtx = payload.Options.NumCtx
	}

	// num_predict: top-level takes precedence over options
	info.NumPredict = payload.NumPredict
	if info.NumPredict == 0 {
		info.NumPredict = payload.Options.NumPredict
	}

	// Collect prompts
	if payload.Prompt != "" {
		info.Prompts = append(info.Prompts, payload.Prompt)
	}
	for _, msg := range payload.Messages {
		if msg.Content != "" {
			info.Prompts = append(info.Prompts, msg.Content)
		}
	}

	return info, nil
}
