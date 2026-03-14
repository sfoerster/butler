package proxy

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

func (p *Proxy) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, p.config.Upstream+"/api/version", nil)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable,
			fmt.Sprintf(`{"status":"unhealthy","error":"%s"}`, "failed to create request"))
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable,
			fmt.Sprintf(`{"status":"unhealthy","error":"%s"}`, "upstream unreachable"))
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		writeJSON(w, http.StatusServiceUnavailable,
			fmt.Sprintf(`{"status":"unhealthy","error":"upstream returned %d"}`, resp.StatusCode))
		return
	}

	writeJSON(w, http.StatusOK, `{"status":"ok"}`)
}
