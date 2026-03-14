package proxy

import (
	"encoding/json"
	"net/http"

	"github.com/sfoerster/butler/internal/auth"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token string `json:"token"`
}

func (p *Proxy) handleLogin(w http.ResponseWriter, r *http.Request) {
	if p.jwt == nil {
		writeJSON(w, http.StatusNotFound, `{"error":"not found"}`)
		return
	}

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, `{"error":"method not allowed"}`)
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, `{"error":"bad request"}`)
		return
	}

	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, `{"error":"username and password are required"}`)
		return
	}

	user := p.config.UserByName(req.Username)
	if user == nil {
		p.logger.Warn("login failed: unknown user",
			"username", req.Username,
			"remote", r.RemoteAddr,
		)
		writeJSON(w, http.StatusUnauthorized, `{"error":"invalid credentials"}`)
		return
	}

	if err := auth.CheckPassword(req.Password, user.PasswordHash); err != nil {
		p.logger.Warn("login failed: wrong password",
			"username", req.Username,
			"remote", r.RemoteAddr,
		)
		writeJSON(w, http.StatusUnauthorized, `{"error":"invalid credentials"}`)
		return
	}

	token, err := p.jwt.Issue(user.Name)
	if err != nil {
		p.logger.Error("failed to issue token",
			"error", err,
			"username", req.Username,
		)
		writeJSON(w, http.StatusInternalServerError, `{"error":"internal error"}`)
		return
	}

	p.logger.Info("login success",
		"username", req.Username,
		"remote", r.RemoteAddr,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(loginResponse{Token: token})
}
