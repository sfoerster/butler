package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseRateLimit(t *testing.T) {
	tests := []struct {
		input     string
		wantCount int
		wantWindow time.Duration
		wantErr   bool
	}{
		{"10/min", 10, time.Minute, false},
		{"600/min", 600, time.Minute, false},
		{"1/min", 1, time.Minute, false},
		{"100/hour", 100, time.Hour, false},
		{"1/hour", 1, time.Hour, false},
		{"0/min", 0, 0, true},
		{"-5/min", 0, 0, true},
		{"10/sec", 0, 0, true},
		{"abc/min", 0, 0, true},
		{"10", 0, 0, true},
		{"", 0, 0, true},
		{"/min", 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			spec, err := ParseRateLimit(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseRateLimit(%q) = %+v, want error", tt.input, spec)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRateLimit(%q) error: %v", tt.input, err)
			}
			if spec.Count != tt.wantCount {
				t.Errorf("Count = %d, want %d", spec.Count, tt.wantCount)
			}
			if spec.Window != tt.wantWindow {
				t.Errorf("Window = %v, want %v", spec.Window, tt.wantWindow)
			}
		})
	}
}

func TestMatchModel(t *testing.T) {
	tests := []struct {
		pattern string
		model   string
		want    bool
	}{
		{"*", "llama3.2", true},
		{"*", "anything:latest", true},
		{"llama3.2", "llama3.2", true},
		{"llama3.2", "llama3.2:latest", true},
		{"llama3.2", "llama3.2:7b", true},
		{"llama3.2", "mistral", false},
		{"llama3.2:7b", "llama3.2:7b", true},
		{"llama3.2:7b", "llama3.2:13b", false},
		{"llama3.2:7b", "llama3.2", false},
		{"mistral", "llama3.2", false},
	}
	for _, tt := range tests {
		got := matchModel(tt.pattern, tt.model)
		if got != tt.want {
			t.Errorf("matchModel(%q, %q) = %v, want %v", tt.pattern, tt.model, got, tt.want)
		}
	}
}

func TestClientModelAllowed(t *testing.T) {
	tests := []struct {
		name   string
		client Client
		model  string
		want   bool
	}{
		{
			name:   "wildcard allows all",
			client: Client{AllowModels: []string{"*"}},
			model:  "anything",
			want:   true,
		},
		{
			name:   "specific model allowed",
			client: Client{AllowModels: []string{"llama3.2", "mistral"}},
			model:  "llama3.2",
			want:   true,
		},
		{
			name:   "model not in allowlist",
			client: Client{AllowModels: []string{"llama3.2"}},
			model:  "mistral",
			want:   false,
		},
		{
			name:   "deny by default when no allowlist",
			client: Client{},
			model:  "llama3.2",
			want:   false,
		},
		{
			name:   "denylist overrides allowlist",
			client: Client{AllowModels: []string{"*"}, DenyModels: []string{"gpt-4"}},
			model:  "gpt-4",
			want:   false,
		},
		{
			name:   "denylist allows other models",
			client: Client{AllowModels: []string{"*"}, DenyModels: []string{"gpt-4"}},
			model:  "llama3.2",
			want:   true,
		},
		{
			name:   "base model deny matches tagged",
			client: Client{AllowModels: []string{"*"}, DenyModels: []string{"llama3.2"}},
			model:  "llama3.2:7b",
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.client.ModelAllowed(tt.model)
			if got != tt.want {
				t.Errorf("ModelAllowed(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	yaml := `
listen: "0.0.0.0:9090"
upstream: "http://localhost:11434"
clients:
  - name: test-client
    key: "sk-test123"
    allow_models: ["llama3.2", "mistral"]
  - name: admin
    key: "sk-admin456"
    allow_models: ["*"]
`
	path := filepath.Join(t.TempDir(), "butler.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Listen != "0.0.0.0:9090" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, "0.0.0.0:9090")
	}
	if cfg.Upstream != "http://localhost:11434" {
		t.Errorf("Upstream = %q, want %q", cfg.Upstream, "http://localhost:11434")
	}
	if len(cfg.Clients) != 2 {
		t.Fatalf("len(Clients) = %d, want 2", len(cfg.Clients))
	}
	if cfg.Clients[0].Name != "test-client" {
		t.Errorf("Clients[0].Name = %q, want %q", cfg.Clients[0].Name, "test-client")
	}
}

func TestLoadWithPhase2Fields(t *testing.T) {
	yaml := `
upstream: "http://localhost:11434"
global_rate_limit: "600/min"
clients:
  - name: test-client
    key: "sk-test"
    allow_models: ["*"]
    rate_limit: "10/min"
    max_request_bytes: 1048576
    max_ctx: 4096
    max_predict: 512
    deny_prompt_patterns:
      - "(?i)ignore.*instructions"
      - "secret.*password"
`
	path := filepath.Join(t.TempDir(), "butler.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.GlobalRate() == nil {
		t.Fatal("GlobalRate() = nil, want non-nil")
	}
	if cfg.GlobalRate().Count != 600 {
		t.Errorf("GlobalRate().Count = %d, want 600", cfg.GlobalRate().Count)
	}
	if cfg.GlobalRate().Window != time.Minute {
		t.Errorf("GlobalRate().Window = %v, want 1m", cfg.GlobalRate().Window)
	}

	cl := &cfg.Clients[0]
	if cl.Rate() == nil {
		t.Fatal("Rate() = nil, want non-nil")
	}
	if cl.Rate().Count != 10 {
		t.Errorf("Rate().Count = %d, want 10", cl.Rate().Count)
	}
	if cl.MaxRequestBytes != 1048576 {
		t.Errorf("MaxRequestBytes = %d, want 1048576", cl.MaxRequestBytes)
	}
	if cl.MaxCtx != 4096 {
		t.Errorf("MaxCtx = %d, want 4096", cl.MaxCtx)
	}
	if cl.MaxPredict != 512 {
		t.Errorf("MaxPredict = %d, want 512", cl.MaxPredict)
	}
	if len(cl.DenyPatterns()) != 2 {
		t.Fatalf("len(DenyPatterns()) = %d, want 2", len(cl.DenyPatterns()))
	}
}

func TestLoadWithPhase3Fields(t *testing.T) {
	yaml := `
upstream: "http://localhost:11434"
log_prompts: true
clients:
  - name: test-client
    key: "sk-test"
    allow_models: ["*"]
`
	path := filepath.Join(t.TempDir(), "butler.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if !cfg.LogPrompts {
		t.Error("LogPrompts = false, want true")
	}
}

func TestLoadWithPhase3FieldsDefault(t *testing.T) {
	yaml := `
upstream: "http://localhost:11434"
clients:
  - name: test-client
    key: "sk-test"
    allow_models: ["*"]
`
	path := filepath.Join(t.TempDir(), "butler.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.LogPrompts {
		t.Error("LogPrompts = true, want false (default)")
	}
}

func TestLoadEnvExpansion(t *testing.T) {
	t.Setenv("TEST_KEY", "sk-from-env")

	yaml := `
upstream: "http://localhost:11434"
clients:
  - name: env-client
    key: "${TEST_KEY}"
    allow_models: ["*"]
`
	path := filepath.Join(t.TempDir(), "butler.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Clients[0].Key != "sk-from-env" {
		t.Errorf("Key = %q, want %q", cfg.Clients[0].Key, "sk-from-env")
	}
}

func TestLoadDefaultListen(t *testing.T) {
	yaml := `
upstream: "http://localhost:11434"
clients:
  - name: test
    key: "sk-test"
    allow_models: ["*"]
`
	path := filepath.Join(t.TempDir(), "butler.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Listen != "127.0.0.1:8080" {
		t.Errorf("Listen = %q, want default %q", cfg.Listen, "127.0.0.1:8080")
	}
}

func TestLoadValidation(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"missing upstream", `clients: [{name: a, key: k, allow_models: ["*"]}]`},
		{"upstream missing scheme", `upstream: "localhost:11434"
clients: [{name: a, key: k, allow_models: ["*"]}]`},
		{"unsupported upstream scheme", `upstream: "ftp://localhost:11434"
clients: [{name: a, key: k, allow_models: ["*"]}]`},
		{"no clients", `upstream: "http://localhost:11434"`},
		{"missing client name", `upstream: "http://localhost:11434"
clients: [{key: k, allow_models: ["*"]}]`},
		{"missing client key", `upstream: "http://localhost:11434"
clients: [{name: a, allow_models: ["*"]}]`},
		{"duplicate key", `upstream: "http://localhost:11434"
clients:
  - {name: a, key: same, allow_models: ["*"]}
  - {name: b, key: same, allow_models: ["*"]}`},
		{"invalid global rate limit", `upstream: "http://localhost:11434"
global_rate_limit: "bad"
clients: [{name: a, key: k, allow_models: ["*"]}]`},
		{"invalid client rate limit", `upstream: "http://localhost:11434"
clients: [{name: a, key: k, allow_models: ["*"], rate_limit: "0/min"}]`},
		{"negative max_request_bytes", `upstream: "http://localhost:11434"
clients: [{name: a, key: k, allow_models: ["*"], max_request_bytes: -1}]`},
		{"negative max_ctx", `upstream: "http://localhost:11434"
clients: [{name: a, key: k, allow_models: ["*"], max_ctx: -1}]`},
		{"negative max_predict", `upstream: "http://localhost:11434"
clients: [{name: a, key: k, allow_models: ["*"], max_predict: -1}]`},
		{"invalid deny_prompt_patterns regex", `upstream: "http://localhost:11434"
clients: [{name: a, key: k, allow_models: ["*"], deny_prompt_patterns: ["[invalid"]}]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "butler.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestLoadDefaultAuthMode(t *testing.T) {
	yaml := `
upstream: "http://localhost:11434"
clients:
  - name: test
    key: "sk-test"
    allow_models: ["*"]
`
	path := filepath.Join(t.TempDir(), "butler.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Auth.Mode != "api_key" {
		t.Errorf("Auth.Mode = %q, want %q", cfg.Auth.Mode, "api_key")
	}
	if cfg.Auth.TokenExpiryDuration() != 24*time.Hour {
		t.Errorf("TokenExpiry = %v, want 24h", cfg.Auth.TokenExpiryDuration())
	}
}

func TestLoadJWTStandaloneMode(t *testing.T) {
	yaml := `
upstream: "http://localhost:11434"
auth:
  mode: jwt_standalone
  jwt_secret: "this-is-a-very-long-secret-key-for-testing-purposes"
  token_expiry: "12h"
users:
  - name: alice
    password_hash: "$2a$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ012"
    allow_models: ["llama3.2"]
    rate_limit: "10/min"
    max_request_bytes: 1048576
    max_ctx: 4096
    max_predict: 512
    deny_prompt_patterns:
      - "(?i)ignore.*instructions"
`
	path := filepath.Join(t.TempDir(), "butler.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Auth.Mode != "jwt_standalone" {
		t.Errorf("Auth.Mode = %q, want %q", cfg.Auth.Mode, "jwt_standalone")
	}
	if cfg.Auth.TokenExpiryDuration() != 12*time.Hour {
		t.Errorf("TokenExpiry = %v, want 12h", cfg.Auth.TokenExpiryDuration())
	}
	if len(cfg.Users) != 1 {
		t.Fatalf("len(Users) = %d, want 1", len(cfg.Users))
	}
	u := &cfg.Users[0]
	if u.Name != "alice" {
		t.Errorf("user name = %q, want %q", u.Name, "alice")
	}
	if u.Rate() == nil {
		t.Fatal("user rate = nil, want non-nil")
	}
	if u.Rate().Count != 10 {
		t.Errorf("user rate count = %d, want 10", u.Rate().Count)
	}
	if u.MaxRequestBytes != 1048576 {
		t.Errorf("MaxRequestBytes = %d, want 1048576", u.MaxRequestBytes)
	}
	if u.MaxCtx != 4096 {
		t.Errorf("MaxCtx = %d, want 4096", u.MaxCtx)
	}
	if u.MaxPredict != 512 {
		t.Errorf("MaxPredict = %d, want 512", u.MaxPredict)
	}
	if len(u.DenyPatterns()) != 1 {
		t.Fatalf("len(DenyPatterns) = %d, want 1", len(u.DenyPatterns()))
	}
}

func TestLoadEitherMode(t *testing.T) {
	yaml := `
upstream: "http://localhost:11434"
auth:
  mode: either
  jwt_secret: "this-is-a-very-long-secret-key-for-testing-purposes"
clients:
  - name: svc
    key: "sk-svc"
    allow_models: ["*"]
users:
  - name: bob
    password_hash: "$2a$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ012"
    allow_models: ["llama3.2"]
`
	path := filepath.Join(t.TempDir(), "butler.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Auth.Mode != "either" {
		t.Errorf("Auth.Mode = %q, want %q", cfg.Auth.Mode, "either")
	}
	if len(cfg.Clients) != 1 {
		t.Errorf("len(Clients) = %d, want 1", len(cfg.Clients))
	}
	if len(cfg.Users) != 1 {
		t.Errorf("len(Users) = %d, want 1", len(cfg.Users))
	}
}

func TestLoadAuthValidation(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"jwt_standalone missing users", `upstream: "http://localhost:11434"
auth:
  mode: jwt_standalone
  jwt_secret: "this-is-a-very-long-secret-key-for-testing-purposes"`},
		{"jwt_standalone missing secret", `upstream: "http://localhost:11434"
auth:
  mode: jwt_standalone
users:
  - name: alice
    password_hash: "$2a$10$hash"
    allow_models: ["*"]`},
		{"jwt_standalone short secret", `upstream: "http://localhost:11434"
auth:
  mode: jwt_standalone
  jwt_secret: "tooshort"
users:
  - name: alice
    password_hash: "$2a$10$hash"
    allow_models: ["*"]`},
		{"either missing secret", `upstream: "http://localhost:11434"
auth:
  mode: either
users:
  - name: alice
    password_hash: "$2a$10$hash"
    allow_models: ["*"]`},
		{"either no clients or users", `upstream: "http://localhost:11434"
auth:
  mode: either
  jwt_secret: "this-is-a-very-long-secret-key-for-testing-purposes"`},
		{"invalid mode", `upstream: "http://localhost:11434"
auth:
  mode: "bogus"
clients:
  - name: a
    key: k
    allow_models: ["*"]`},
		{"bad token_expiry", `upstream: "http://localhost:11434"
auth:
  mode: jwt_standalone
  jwt_secret: "this-is-a-very-long-secret-key-for-testing-purposes"
  token_expiry: "bad"
users:
  - name: a
    password_hash: "$2a$10$hash"
    allow_models: ["*"]`},
		{"negative token_expiry", `upstream: "http://localhost:11434"
auth:
  mode: jwt_standalone
  jwt_secret: "this-is-a-very-long-secret-key-for-testing-purposes"
  token_expiry: "-1h"
users:
  - name: a
    password_hash: "$2a$10$hash"
    allow_models: ["*"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "butler.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestLoadUserValidation(t *testing.T) {
	base := func(users string) string {
		return `upstream: "http://localhost:11434"
auth:
  mode: jwt_standalone
  jwt_secret: "this-is-a-very-long-secret-key-for-testing-purposes"
users:
` + users
	}

	tests := []struct {
		name  string
		users string
	}{
		{"missing name", `  - password_hash: "$2a$10$hash"
    allow_models: ["*"]`},
		{"missing password_hash", `  - name: alice
    allow_models: ["*"]`},
		{"duplicate name", `  - name: alice
    password_hash: "$2a$10$hash1"
    allow_models: ["*"]
  - name: alice
    password_hash: "$2a$10$hash2"
    allow_models: ["*"]`},
		{"negative max_request_bytes", `  - name: alice
    password_hash: "$2a$10$hash"
    allow_models: ["*"]
    max_request_bytes: -1`},
		{"negative max_ctx", `  - name: alice
    password_hash: "$2a$10$hash"
    allow_models: ["*"]
    max_ctx: -1`},
		{"negative max_predict", `  - name: alice
    password_hash: "$2a$10$hash"
    allow_models: ["*"]
    max_predict: -1`},
		{"bad rate_limit", `  - name: alice
    password_hash: "$2a$10$hash"
    allow_models: ["*"]
    rate_limit: "bad"`},
		{"bad regex", `  - name: alice
    password_hash: "$2a$10$hash"
    allow_models: ["*"]
    deny_prompt_patterns: ["[invalid"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "butler.yaml")
			if err := os.WriteFile(path, []byte(base(tt.users)), 0644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestUserByName(t *testing.T) {
	cfg := &Config{
		Users: []User{
			{Name: "alice"},
			{Name: "bob"},
		},
	}

	if u := cfg.UserByName("alice"); u == nil || u.Name != "alice" {
		t.Error("expected alice")
	}
	if u := cfg.UserByName("bob"); u == nil || u.Name != "bob" {
		t.Error("expected bob")
	}
	if u := cfg.UserByName("charlie"); u != nil {
		t.Error("expected nil for unknown user")
	}
}

func TestUserSubject(t *testing.T) {
	rate := &RateSpec{Count: 10, Window: time.Minute}
	u := User{
		Name:            "alice",
		AllowModels:     []string{"llama3.2"},
		DenyModels:      []string{"gpt-4"},
		MaxRequestBytes: 1024,
		MaxCtx:          4096,
		MaxPredict:      512,
	}
	u.SetRateForTest(rate)

	s := u.Subject()
	if s.Name != "alice" {
		t.Errorf("Name = %q, want alice", s.Name)
	}
	if s.AuthSource != "jwt" {
		t.Errorf("AuthSource = %q, want jwt", s.AuthSource)
	}
	if s.Rate != rate {
		t.Error("Rate not carried through")
	}
	if s.MaxReqBytes != 1024 {
		t.Errorf("MaxReqBytes = %d, want 1024", s.MaxReqBytes)
	}
	if s.MaxCtx != 4096 {
		t.Errorf("MaxCtx = %d, want 4096", s.MaxCtx)
	}
	if s.MaxPredict != 512 {
		t.Errorf("MaxPredict = %d, want 512", s.MaxPredict)
	}
}

func TestClientSubject(t *testing.T) {
	cl := Client{
		Name:        "svc",
		AllowModels: []string{"*"},
	}
	s := cl.Subject()
	if s.AuthSource != "api_key" {
		t.Errorf("AuthSource = %q, want api_key", s.AuthSource)
	}
}

func TestSubjectModelAllowed(t *testing.T) {
	tests := []struct {
		name    string
		subject Subject
		model   string
		want    bool
	}{
		{"wildcard allows all", Subject{AllowModels: []string{"*"}}, "anything", true},
		{"specific allowed", Subject{AllowModels: []string{"llama3.2"}}, "llama3.2", true},
		{"not in allowlist", Subject{AllowModels: []string{"llama3.2"}}, "mistral", false},
		{"deny by default", Subject{}, "llama3.2", false},
		{"deny overrides allow", Subject{AllowModels: []string{"*"}, DenyModels: []string{"gpt-4"}}, "gpt-4", false},
		{"deny allows others", Subject{AllowModels: []string{"*"}, DenyModels: []string{"gpt-4"}}, "llama3.2", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.subject.ModelAllowed(tt.model); got != tt.want {
				t.Errorf("ModelAllowed(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

func TestSubjectRateLimitKey(t *testing.T) {
	apiKey := Subject{Name: "svc", AuthSource: "api_key"}
	jwt := Subject{Name: "svc", AuthSource: "jwt"}

	if apiKey.RateLimitKey() == jwt.RateLimitKey() {
		t.Errorf("rate limit keys should differ: %q == %q", apiKey.RateLimitKey(), jwt.RateLimitKey())
	}
	if apiKey.RateLimitKey() != "api_key:svc" {
		t.Errorf("api_key key = %q, want api_key:svc", apiKey.RateLimitKey())
	}
	if jwt.RateLimitKey() != "jwt:svc" {
		t.Errorf("jwt key = %q, want jwt:svc", jwt.RateLimitKey())
	}
}

func TestClientByKey(t *testing.T) {
	cfg := &Config{
		Clients: []Client{
			{Name: "alice", Key: "sk-alice"},
			{Name: "bob", Key: "sk-bob"},
		},
	}

	if c := cfg.ClientByKey("sk-alice"); c == nil || c.Name != "alice" {
		t.Error("expected alice")
	}
	if c := cfg.ClientByKey("sk-bob"); c == nil || c.Name != "bob" {
		t.Error("expected bob")
	}
	if c := cfg.ClientByKey("sk-unknown"); c != nil {
		t.Error("expected nil for unknown key")
	}
}

func TestLoadOIDCMode(t *testing.T) {
	yaml := `
upstream: "http://localhost:11434"
auth:
  mode: oidc
  oidc:
    issuer: "https://auth.example.com/realms/default"
    client_id: "butler"
    role_claim_path: "realm_access.roles"
    refresh_interval: "30m"
role_policies:
  admin:
    allow_models: ["*"]
  viewer:
    allow_models: ["llama3.2:1b"]
    rate_limit: "20/hour"
    max_ctx: 2048
`
	path := filepath.Join(t.TempDir(), "butler.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Auth.Mode != "oidc" {
		t.Errorf("Auth.Mode = %q, want %q", cfg.Auth.Mode, "oidc")
	}
	if cfg.Auth.OIDC.Issuer != "https://auth.example.com/realms/default" {
		t.Errorf("OIDC.Issuer = %q", cfg.Auth.OIDC.Issuer)
	}
	if cfg.Auth.OIDC.ClientID != "butler" {
		t.Errorf("OIDC.ClientID = %q", cfg.Auth.OIDC.ClientID)
	}
	if cfg.Auth.OIDC.RoleClaimPath != "realm_access.roles" {
		t.Errorf("OIDC.RoleClaimPath = %q", cfg.Auth.OIDC.RoleClaimPath)
	}
	if cfg.Auth.OIDC.RefreshIntervalDuration() != 30*time.Minute {
		t.Errorf("RefreshInterval = %v, want 30m", cfg.Auth.OIDC.RefreshIntervalDuration())
	}
	if len(cfg.RolePolicies) != 2 {
		t.Fatalf("len(RolePolicies) = %d, want 2", len(cfg.RolePolicies))
	}
	admin := cfg.RolePolicies["admin"]
	if len(admin.AllowModels) != 1 || admin.AllowModels[0] != "*" {
		t.Errorf("admin AllowModels = %v", admin.AllowModels)
	}
	viewer := cfg.RolePolicies["viewer"]
	if viewer.rate == nil || viewer.rate.Count != 20 {
		t.Errorf("viewer rate = %+v", viewer.rate)
	}
	if viewer.MaxCtx != 2048 {
		t.Errorf("viewer MaxCtx = %d", viewer.MaxCtx)
	}
}

func TestLoadOIDCValidation(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"missing issuer", `upstream: "http://localhost:11434"
auth:
  mode: oidc
  oidc:
    client_id: "butler"
    role_claim_path: "roles"
role_policies:
  admin:
    allow_models: ["*"]`},
		{"non-HTTPS issuer", `upstream: "http://localhost:11434"
auth:
  mode: oidc
  oidc:
    issuer: "http://auth.example.com"
    client_id: "butler"
    role_claim_path: "roles"
role_policies:
  admin:
    allow_models: ["*"]`},
		{"missing client_id", `upstream: "http://localhost:11434"
auth:
  mode: oidc
  oidc:
    issuer: "https://auth.example.com"
    role_claim_path: "roles"
role_policies:
  admin:
    allow_models: ["*"]`},
		{"missing role_claim_path", `upstream: "http://localhost:11434"
auth:
  mode: oidc
  oidc:
    issuer: "https://auth.example.com"
    client_id: "butler"
role_policies:
  admin:
    allow_models: ["*"]`},
		{"no role_policies", `upstream: "http://localhost:11434"
auth:
  mode: oidc
  oidc:
    issuer: "https://auth.example.com"
    client_id: "butler"
    role_claim_path: "roles"`},
		{"bad refresh_interval", `upstream: "http://localhost:11434"
auth:
  mode: oidc
  oidc:
    issuer: "https://auth.example.com"
    client_id: "butler"
    role_claim_path: "roles"
    refresh_interval: "bad"
role_policies:
  admin:
    allow_models: ["*"]`},
		{"negative refresh_interval", `upstream: "http://localhost:11434"
auth:
  mode: oidc
  oidc:
    issuer: "https://auth.example.com"
    client_id: "butler"
    role_claim_path: "roles"
    refresh_interval: "-1m"
role_policies:
  admin:
    allow_models: ["*"]`},
		{"missing oidc config", `upstream: "http://localhost:11434"
auth:
  mode: oidc
role_policies:
  admin:
    allow_models: ["*"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "butler.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestLoadEitherModeWithOIDC(t *testing.T) {
	yaml := `
upstream: "http://localhost:11434"
auth:
  mode: either
  oidc:
    issuer: "https://auth.example.com/realms/default"
    client_id: "butler"
    role_claim_path: "realm_access.roles"
clients:
  - name: svc
    key: "sk-svc"
    allow_models: ["*"]
role_policies:
  admin:
    allow_models: ["*"]
`
	path := filepath.Join(t.TempDir(), "butler.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Auth.Mode != "either" {
		t.Errorf("Auth.Mode = %q, want either", cfg.Auth.Mode)
	}
	if cfg.Auth.OIDC == nil {
		t.Fatal("OIDC config should be present")
	}
	if cfg.Auth.JWTSecret != "" {
		// No jwt_secret required when only OIDC + API keys
	}
}

func TestRolePolicyValidation(t *testing.T) {
	base := func(policy string) string {
		return `upstream: "http://localhost:11434"
auth:
  mode: oidc
  oidc:
    issuer: "https://auth.example.com"
    client_id: "butler"
    role_claim_path: "roles"
role_policies:
` + policy
	}

	tests := []struct {
		name   string
		policy string
	}{
		{"bad rate_limit", `  admin:
    allow_models: ["*"]
    rate_limit: "bad"`},
		{"negative max_request_bytes", `  admin:
    allow_models: ["*"]
    max_request_bytes: -1`},
		{"negative max_ctx", `  admin:
    allow_models: ["*"]
    max_ctx: -1`},
		{"negative max_predict", `  admin:
    allow_models: ["*"]
    max_predict: -1`},
		{"bad regex in deny_prompt_patterns", `  admin:
    allow_models: ["*"]
    deny_prompt_patterns: ["[invalid"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "butler.yaml")
			if err := os.WriteFile(path, []byte(base(tt.policy)), 0644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestRolePolicyUnlimited(t *testing.T) {
	yaml := `
upstream: "http://localhost:11434"
auth:
  mode: oidc
  oidc:
    issuer: "https://auth.example.com"
    client_id: "butler"
    role_claim_path: "roles"
role_policies:
  admin:
    allow_models: ["*"]
    rate_limit: "unlimited"
`
	path := filepath.Join(t.TempDir(), "butler.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	admin := cfg.RolePolicies["admin"]
	if !admin.unlimited {
		t.Error("admin should be unlimited")
	}
	if admin.rate != nil {
		t.Error("admin rate should be nil for unlimited")
	}
}

func TestSubjectFromRolesSingle(t *testing.T) {
	cfg := &Config{
		RolePolicies: map[string]RolePolicy{
			"viewer": {
				AllowModels: []string{"llama3.2:1b"},
				rate:        &RateSpec{Count: 20, Window: time.Hour},
				MaxCtx:      2048,
				MaxPredict:  256,
			},
		},
	}

	subj := cfg.SubjectFromRoles("alice", []string{"viewer"})
	if subj == nil {
		t.Fatal("expected non-nil subject")
	}
	if subj.Name != "alice" {
		t.Errorf("Name = %q", subj.Name)
	}
	if subj.AuthSource != "oidc" {
		t.Errorf("AuthSource = %q", subj.AuthSource)
	}
	if len(subj.AllowModels) != 1 || subj.AllowModels[0] != "llama3.2:1b" {
		t.Errorf("AllowModels = %v", subj.AllowModels)
	}
	if subj.Rate == nil || subj.Rate.Count != 20 {
		t.Errorf("Rate = %+v", subj.Rate)
	}
	if subj.MaxCtx != 2048 {
		t.Errorf("MaxCtx = %d", subj.MaxCtx)
	}
	if subj.MaxPredict != 256 {
		t.Errorf("MaxPredict = %d", subj.MaxPredict)
	}
}

func TestSubjectFromRolesMultiple(t *testing.T) {
	cfg := &Config{
		RolePolicies: map[string]RolePolicy{
			"viewer": {
				AllowModels: []string{"llama3.2:1b"},
				rate:        &RateSpec{Count: 20, Window: time.Hour},
				MaxCtx:      2048,
			},
			"operator": {
				AllowModels: []string{"llama3.2", "mistral"},
				rate:        &RateSpec{Count: 120, Window: time.Hour},
				MaxCtx:      4096,
			},
		},
	}

	subj := cfg.SubjectFromRoles("bob", []string{"viewer", "operator"})
	if subj == nil {
		t.Fatal("expected non-nil subject")
	}

	// AllowModels should be union
	allowMap := make(map[string]bool)
	for _, m := range subj.AllowModels {
		allowMap[m] = true
	}
	if !allowMap["llama3.2:1b"] || !allowMap["llama3.2"] || !allowMap["mistral"] {
		t.Errorf("AllowModels union = %v", subj.AllowModels)
	}

	// Rate should be most permissive (120/hour > 20/hour)
	if subj.Rate == nil || subj.Rate.Count != 120 {
		t.Errorf("Rate = %+v, want 120/hour", subj.Rate)
	}

	// MaxCtx should be most permissive (4096 > 2048)
	if subj.MaxCtx != 4096 {
		t.Errorf("MaxCtx = %d, want 4096", subj.MaxCtx)
	}

	// DenyModels/DenyPatterns should be empty (multi-role)
	if len(subj.DenyModels) != 0 {
		t.Errorf("DenyModels should be empty for multi-role, got %v", subj.DenyModels)
	}
}

func TestSubjectFromRolesUnlimited(t *testing.T) {
	cfg := &Config{
		RolePolicies: map[string]RolePolicy{
			"viewer": {
				AllowModels: []string{"llama3.2"},
				rate:        &RateSpec{Count: 20, Window: time.Hour},
			},
			"admin": {
				AllowModels: []string{"*"},
				unlimited:   true,
			},
		},
	}

	subj := cfg.SubjectFromRoles("charlie", []string{"viewer", "admin"})
	if subj == nil {
		t.Fatal("expected non-nil subject")
	}

	// Wildcard should win
	if len(subj.AllowModels) != 1 || subj.AllowModels[0] != "*" {
		t.Errorf("AllowModels = %v, want [*]", subj.AllowModels)
	}

	// Unlimited rate should win (nil)
	if subj.Rate != nil {
		t.Errorf("Rate = %+v, want nil (unlimited)", subj.Rate)
	}
}

func TestSubjectFromRolesNoMatch(t *testing.T) {
	cfg := &Config{
		RolePolicies: map[string]RolePolicy{
			"admin": {
				AllowModels: []string{"*"},
			},
		},
	}

	subj := cfg.SubjectFromRoles("nobody", []string{"nonexistent", "other"})
	if subj != nil {
		t.Errorf("expected nil, got %+v", subj)
	}
}

func TestOIDCRefreshIntervalDefault(t *testing.T) {
	oidc := &OIDCConfig{}
	if oidc.RefreshIntervalDuration() != 60*time.Minute {
		t.Errorf("default RefreshInterval = %v, want 60m", oidc.RefreshIntervalDuration())
	}
}
