package config

import (
	"os"
	"path/filepath"
	"testing"
)

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
