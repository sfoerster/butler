package proxy

import (
	"encoding/json"
	"io"
	"net/http"
)

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

// extractModel reads the model field from the request body for model-bearing endpoints.
// Returns the model name, the full body bytes (for re-reading), and any error.
// For non-model-bearing endpoints, returns ("", nil, nil).
func extractModel(r *http.Request) (string, []byte, error) {
	if !modelBearingPaths[r.URL.Path] {
		return "", nil, nil
	}
	if r.Body == nil {
		return "", nil, nil
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return "", nil, err
	}
	_ = r.Body.Close()

	var payload struct {
		Model string `json:"model"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		// Non-JSON body — let upstream handle it
		return "", body, nil
	}

	model := payload.Model
	if model == "" {
		model = payload.Name
	}

	return model, body, nil
}
