package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// testOIDCProvider sets up a mock OIDC provider with discovery + JWKS endpoints.
type testOIDCProvider struct {
	server    *httptest.Server
	rsaKey    *rsa.PrivateKey
	kid       string
	issuer    string
	jwksHits  atomic.Int64
	jwksErr   atomic.Bool // if true, JWKS endpoint returns 500
	extraKeys []jwkKey
}

func newTestOIDCProvider(t *testing.T) *testOIDCProvider {
	t.Helper()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	p := &testOIDCProvider{
		rsaKey: rsaKey,
		kid:    "test-key-1",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.handleDiscovery)
	mux.HandleFunc("/jwks", p.handleJWKS)

	p.server = httptest.NewServer(mux)
	p.issuer = p.server.URL
	return p
}

func (p *testOIDCProvider) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"issuer":   p.issuer,
		"jwks_uri": p.server.URL + "/jwks",
	})
}

func (p *testOIDCProvider) handleJWKS(w http.ResponseWriter, r *http.Request) {
	p.jwksHits.Add(1)
	if p.jwksErr.Load() {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	keys := []jwkKey{p.rsaJWK()}
	keys = append(keys, p.extraKeys...)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"keys": keys,
	})
}

func (p *testOIDCProvider) rsaJWK() jwkKey {
	return jwkKey{
		Kty: "RSA",
		Kid: p.kid,
		Use: "sig",
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(p.rsaKey.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(p.rsaKey.E)).Bytes()),
	}
}

func (p *testOIDCProvider) signToken(claims jwt.MapClaims) string {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = p.kid
	signed, err := token.SignedString(p.rsaKey)
	if err != nil {
		panic(err)
	}
	return signed
}

func (p *testOIDCProvider) validClaims(roles []string) jwt.MapClaims {
	return jwt.MapClaims{
		"iss":                p.issuer,
		"aud":                "butler",
		"sub":                "user123",
		"preferred_username": "alice",
		"exp":                time.Now().Add(time.Hour).Unix(),
		"iat":                time.Now().Unix(),
		"realm_access": map[string]interface{}{
			"roles": toInterfaceSlice(roles),
		},
	}
}

func toInterfaceSlice(ss []string) []interface{} {
	out := make([]interface{}, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func (p *testOIDCProvider) close() {
	p.server.Close()
}

func newTestOIDCService(t *testing.T, p *testOIDCProvider) *OIDCService {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	svc, err := NewOIDCService(p.issuer, "butler", "realm_access.roles", 60*time.Minute, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Stop)
	return svc
}

func TestOIDCValidateSuccess(t *testing.T) {
	p := newTestOIDCProvider(t)
	defer p.close()
	svc := newTestOIDCService(t, p)

	token := p.signToken(p.validClaims([]string{"admin", "viewer"}))
	claims, err := svc.Validate(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "user123" {
		t.Errorf("Subject = %q", claims.Subject)
	}
	if claims.PreferredUsername != "alice" {
		t.Errorf("PreferredUsername = %q", claims.PreferredUsername)
	}
	if len(claims.Roles) != 2 {
		t.Fatalf("len(Roles) = %d, want 2", len(claims.Roles))
	}
	if claims.Roles[0] != "admin" || claims.Roles[1] != "viewer" {
		t.Errorf("Roles = %v", claims.Roles)
	}
}

func TestOIDCValidateExpired(t *testing.T) {
	p := newTestOIDCProvider(t)
	defer p.close()
	svc := newTestOIDCService(t, p)

	claims := p.validClaims([]string{"admin"})
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	token := p.signToken(claims)

	_, err := svc.Validate(token)
	if err != ErrOIDCExpiredToken {
		t.Errorf("err = %v, want ErrOIDCExpiredToken", err)
	}
}

func TestOIDCValidateWrongIssuer(t *testing.T) {
	p := newTestOIDCProvider(t)
	defer p.close()
	svc := newTestOIDCService(t, p)

	claims := p.validClaims([]string{"admin"})
	claims["iss"] = "https://wrong.example.com"
	token := p.signToken(claims)

	_, err := svc.Validate(token)
	if err == nil {
		t.Error("expected error for wrong issuer")
	}
}

func TestOIDCValidateWrongAudience(t *testing.T) {
	p := newTestOIDCProvider(t)
	defer p.close()
	svc := newTestOIDCService(t, p)

	claims := p.validClaims([]string{"admin"})
	claims["aud"] = "wrong-client"
	token := p.signToken(claims)

	_, err := svc.Validate(token)
	if err == nil {
		t.Error("expected error for wrong audience")
	}
}

func TestOIDCValidateNoSub(t *testing.T) {
	p := newTestOIDCProvider(t)
	defer p.close()
	svc := newTestOIDCService(t, p)

	claims := p.validClaims([]string{"admin"})
	delete(claims, "sub")
	token := p.signToken(claims)

	_, err := svc.Validate(token)
	if err == nil {
		t.Error("expected error for missing sub")
	}
}

func TestOIDCValidateHS256Rejected(t *testing.T) {
	p := newTestOIDCProvider(t)
	defer p.close()
	svc := newTestOIDCService(t, p)

	// Sign with HMAC instead of RSA
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, p.validClaims([]string{"admin"}))
	signed, err := token.SignedString([]byte("some-secret-that-is-long-enough!!"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Validate(signed)
	if err == nil {
		t.Error("expected error for HS256 token")
	}
}

func TestOIDCValidateWrongSignature(t *testing.T) {
	p := newTestOIDCProvider(t)
	defer p.close()
	svc := newTestOIDCService(t, p)

	// Sign with a different RSA key
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, p.validClaims([]string{"admin"}))
	token.Header["kid"] = p.kid
	signed, err := token.SignedString(wrongKey)
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Validate(signed)
	if err == nil {
		t.Error("expected error for wrong signature")
	}
}

func TestOIDCExtractRolesNested(t *testing.T) {
	p := newTestOIDCProvider(t)
	defer p.close()
	svc := newTestOIDCService(t, p)

	claims := p.validClaims([]string{"admin", "operator"})
	token := p.signToken(claims)

	result, err := svc.Validate(token)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Roles) != 2 || result.Roles[0] != "admin" || result.Roles[1] != "operator" {
		t.Errorf("Roles = %v, want [admin operator]", result.Roles)
	}
}

func TestOIDCExtractRolesFlat(t *testing.T) {
	p := newTestOIDCProvider(t)
	defer p.close()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	svc, err := NewOIDCService(p.issuer, "butler", "groups", 60*time.Minute, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()

	claims := jwt.MapClaims{
		"iss":    p.issuer,
		"aud":    "butler",
		"sub":    "user456",
		"exp":    time.Now().Add(time.Hour).Unix(),
		"iat":    time.Now().Unix(),
		"groups": []interface{}{"devs", "ops"},
	}
	token := p.signToken(claims)

	result, err := svc.Validate(token)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Roles) != 2 || result.Roles[0] != "devs" || result.Roles[1] != "ops" {
		t.Errorf("Roles = %v, want [devs ops]", result.Roles)
	}
}

func TestOIDCExtractRolesMissing(t *testing.T) {
	p := newTestOIDCProvider(t)
	defer p.close()
	svc := newTestOIDCService(t, p)

	claims := jwt.MapClaims{
		"iss": p.issuer,
		"aud": "butler",
		"sub": "user789",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
		// No realm_access.roles
	}
	token := p.signToken(claims)

	_, err := svc.Validate(token)
	if err != ErrOIDCNoRoles {
		t.Errorf("err = %v, want ErrOIDCNoRoles", err)
	}
}

func TestOIDCKeyRotation(t *testing.T) {
	p := newTestOIDCProvider(t)
	defer p.close()
	svc := newTestOIDCService(t, p)

	// Set minRefreshGap to 0 so rotation refresh is immediate
	svc.minRefreshGap = 0

	// Generate new key and update provider
	newKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	newKid := "test-key-2"
	p.rsaKey = newKey
	p.kid = newKid

	// Sign token with new key
	token := p.signToken(p.validClaims([]string{"admin"}))

	// Should trigger JWKS refresh and succeed
	claims, err := svc.Validate(token)
	if err != nil {
		t.Fatalf("key rotation validation failed: %v", err)
	}
	if claims.Subject != "user123" {
		t.Errorf("Subject = %q", claims.Subject)
	}
}

func TestOIDCCachedKeysAfterProviderDown(t *testing.T) {
	p := newTestOIDCProvider(t)
	defer p.close()
	svc := newTestOIDCService(t, p)

	// Valid token works
	token := p.signToken(p.validClaims([]string{"admin"}))
	_, err := svc.Validate(token)
	if err != nil {
		t.Fatal(err)
	}

	// Provider goes down
	p.jwksErr.Store(true)

	// Cached key should still work with same token
	_, err = svc.Validate(token)
	if err != nil {
		t.Fatalf("cached key should work after provider down: %v", err)
	}
}

func TestOIDCParseRSAKey(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	jwk := jwkKey{
		Kty: "RSA",
		Kid: "rsa-1",
		Use: "sig",
		N:   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}

	pub, err := parseRSAKey(jwk)
	if err != nil {
		t.Fatal(err)
	}
	if pub.N.Cmp(key.N) != 0 {
		t.Error("N mismatch")
	}
	if pub.E != key.E {
		t.Errorf("E = %d, want %d", pub.E, key.E)
	}
}

func TestOIDCParseECKey(t *testing.T) {
	curves := []struct {
		name  string
		curve elliptic.Curve
		crv   string
	}{
		{"P-256", elliptic.P256(), "P-256"},
		{"P-384", elliptic.P384(), "P-384"},
	}

	for _, tc := range curves {
		t.Run(tc.name, func(t *testing.T) {
			key, err := ecdsa.GenerateKey(tc.curve, rand.Reader)
			if err != nil {
				t.Fatal(err)
			}

			jwk := jwkKey{
				Kty: "EC",
				Kid: fmt.Sprintf("ec-%s", tc.crv),
				Use: "sig",
				Crv: tc.crv,
				X:   base64.RawURLEncoding.EncodeToString(key.X.Bytes()),
				Y:   base64.RawURLEncoding.EncodeToString(key.Y.Bytes()),
			}

			pub, err := parseECKey(jwk)
			if err != nil {
				t.Fatal(err)
			}
			if pub.X.Cmp(key.X) != 0 {
				t.Error("X mismatch")
			}
			if pub.Y.Cmp(key.Y) != 0 {
				t.Error("Y mismatch")
			}
			if pub.Curve != tc.curve {
				t.Error("curve mismatch")
			}
		})
	}
}
