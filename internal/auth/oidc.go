package auth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrOIDCInvalidToken       = errors.New("invalid OIDC token")
	ErrOIDCExpiredToken       = errors.New("OIDC token expired")
	ErrOIDCNoRoles            = errors.New("no roles found in token")
	ErrOIDCProviderUnreachable = errors.New("OIDC provider unreachable")
)

// OIDCClaims holds extracted claims from a validated OIDC token.
type OIDCClaims struct {
	Subject           string
	PreferredUsername string
	Roles             []string
}

// OIDCService handles OIDC token validation using JWKS auto-discovery.
type OIDCService struct {
	issuer          string
	clientID        string
	roleClaimPath   []string
	jwksURL         string
	mu              sync.RWMutex
	keys            map[string]crypto.PublicKey
	lastRefresh     time.Time
	refreshInterval time.Duration
	minRefreshGap   time.Duration
	httpClient      *http.Client
	logger          *slog.Logger
	done            chan struct{}
}

// NewOIDCService creates a new OIDCService. It performs OIDC discovery and fetches
// the initial JWKS on creation. Returns an error if the provider is unreachable.
func NewOIDCService(issuer, clientID, roleClaimPath string, refreshInterval time.Duration, logger *slog.Logger) (*OIDCService, error) {
	s := &OIDCService{
		issuer:          issuer,
		clientID:        clientID,
		roleClaimPath:   strings.Split(roleClaimPath, "."),
		refreshInterval: refreshInterval,
		minRefreshGap:   30 * time.Second,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
		done:   make(chan struct{}),
		keys:   make(map[string]crypto.PublicKey),
	}

	// Discover JWKS URL
	jwksURL, err := s.fetchDiscovery()
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery: %w", err)
	}
	s.jwksURL = jwksURL

	// Fetch initial JWKS
	if err := s.refreshJWKS(); err != nil {
		return nil, fmt.Errorf("OIDC JWKS fetch: %w", err)
	}

	// Start background refresh
	go s.backgroundRefresh()

	return s, nil
}

// Stop stops the background JWKS refresh goroutine.
func (s *OIDCService) Stop() {
	close(s.done)
}

// Validate parses and validates an OIDC token. Returns claims on success.
func (s *OIDCService) Validate(tokenString string) (*OIDCClaims, error) {
	token, err := jwt.Parse(tokenString, s.keyFunc,
		jwt.WithIssuer(s.issuer),
		jwt.WithAudience(s.clientID),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrOIDCExpiredToken
		}
		return nil, ErrOIDCInvalidToken
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, ErrOIDCInvalidToken
	}

	sub, _ := claims.GetSubject()
	if sub == "" {
		return nil, ErrOIDCInvalidToken
	}

	preferredUsername, _ := claims["preferred_username"].(string)

	roles, err := extractRoles(claims, s.roleClaimPath)
	if err != nil {
		return nil, err
	}

	return &OIDCClaims{
		Subject:           sub,
		PreferredUsername: preferredUsername,
		Roles:             roles,
	}, nil
}

type openidConfiguration struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

func (s *OIDCService) fetchDiscovery() (string, error) {
	discoveryURL := strings.TrimRight(s.issuer, "/") + "/.well-known/openid-configuration"
	resp, err := s.httpClient.Get(discoveryURL)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrOIDCProviderUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: discovery returned %d", ErrOIDCProviderUnreachable, resp.StatusCode)
	}

	var config openidConfiguration
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return "", fmt.Errorf("parsing discovery document: %w", err)
	}

	if config.Issuer != s.issuer {
		return "", fmt.Errorf("issuer mismatch: discovery says %q, config says %q", config.Issuer, s.issuer)
	}

	if config.JWKSURI == "" {
		return "", fmt.Errorf("discovery document missing jwks_uri")
	}

	return config.JWKSURI, nil
}

type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`

	// RSA
	N string `json:"n"`
	E string `json:"e"`

	// EC
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (s *OIDCService) refreshJWKS() error {
	resp, err := s.httpClient.Get(s.jwksURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOIDCProviderUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: JWKS returned %d", ErrOIDCProviderUnreachable, resp.StatusCode)
	}

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("parsing JWKS: %w", err)
	}

	keys, err := parseJWKS(jwks.Keys)
	if err != nil {
		return fmt.Errorf("parsing JWKS keys: %w", err)
	}

	s.mu.Lock()
	s.keys = keys
	s.lastRefresh = time.Now()
	s.mu.Unlock()

	s.logger.Info("JWKS refreshed", "keys", len(keys))
	return nil
}

func parseJWKS(jwkKeys []jwkKey) (map[string]crypto.PublicKey, error) {
	keys := make(map[string]crypto.PublicKey)
	for _, k := range jwkKeys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		switch k.Kty {
		case "RSA":
			pub, err := parseRSAKey(k)
			if err != nil {
				return nil, fmt.Errorf("kid %q: %w", k.Kid, err)
			}
			keys[k.Kid] = pub
		case "EC":
			pub, err := parseECKey(k)
			if err != nil {
				return nil, fmt.Errorf("kid %q: %w", k.Kid, err)
			}
			keys[k.Kid] = pub
		}
	}
	return keys, nil
}

func parseRSAKey(k jwkKey) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decoding n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decoding e: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := 0
	for _, b := range eBytes {
		e = e<<8 + int(b)
	}

	return &rsa.PublicKey{
		N: n,
		E: e,
	}, nil
}

func parseECKey(k jwkKey) (*ecdsa.PublicKey, error) {
	var curve elliptic.Curve
	switch k.Crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, fmt.Errorf("unsupported curve: %s", k.Crv)
	}

	xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, fmt.Errorf("decoding x: %w", err)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, fmt.Errorf("decoding y: %w", err)
	}

	return &ecdsa.PublicKey{
		Curve: curve,
		X:     new(big.Int).SetBytes(xBytes),
		Y:     new(big.Int).SetBytes(yBytes),
	}, nil
}

func (s *OIDCService) keyFunc(token *jwt.Token) (interface{}, error) {
	// Reject HMAC and none algorithms
	if _, ok := token.Method.(*jwt.SigningMethodHMAC); ok {
		return nil, ErrOIDCInvalidToken
	}
	if token.Method.Alg() == "none" {
		return nil, ErrOIDCInvalidToken
	}

	kid, ok := token.Header["kid"].(string)
	if !ok || kid == "" {
		return nil, ErrOIDCInvalidToken
	}

	// Try cached key
	s.mu.RLock()
	key, found := s.keys[kid]
	s.mu.RUnlock()

	if found {
		return key, nil
	}

	// Unknown kid — try refreshing
	if err := s.maybeRefresh(); err != nil {
		s.logger.Warn("JWKS refresh on unknown kid failed", "error", err, "kid", kid)
	}

	s.mu.RLock()
	key, found = s.keys[kid]
	s.mu.RUnlock()

	if !found {
		return nil, ErrOIDCInvalidToken
	}
	return key, nil
}

func (s *OIDCService) maybeRefresh() error {
	s.mu.RLock()
	elapsed := time.Since(s.lastRefresh)
	s.mu.RUnlock()

	if elapsed < s.minRefreshGap {
		return nil
	}

	return s.refreshJWKS()
}

func (s *OIDCService) backgroundRefresh() {
	ticker := time.NewTicker(s.refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			if err := s.refreshJWKS(); err != nil {
				s.logger.Warn("background JWKS refresh failed, keeping cached keys", "error", err)
			}
		}
	}
}

func extractRoles(claims jwt.MapClaims, path []string) ([]string, error) {
	var current interface{} = map[string]interface{}(claims)

	for _, segment := range path {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil, ErrOIDCNoRoles
		}
		current, ok = m[segment]
		if !ok {
			return nil, ErrOIDCNoRoles
		}
	}

	// current should be a []interface{} of strings
	arr, ok := current.([]interface{})
	if !ok {
		return nil, ErrOIDCNoRoles
	}

	roles := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			roles = append(roles, s)
		}
	}

	if len(roles) == 0 {
		return nil, ErrOIDCNoRoles
	}

	return roles, nil
}
