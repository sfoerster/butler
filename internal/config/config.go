package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen   string   `yaml:"listen"`
	Upstream string   `yaml:"upstream"`
	Clients  []Client `yaml:"clients"`
}

type Client struct {
	Name        string   `yaml:"name"`
	Key         string   `yaml:"key"`
	AllowModels []string `yaml:"allow_models"`
	DenyModels  []string `yaml:"deny_models"`
}

// Load reads and parses the config file, expanding ${VAR} environment variables.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8080"
	}
	if c.Upstream == "" {
		return fmt.Errorf("upstream is required")
	}
	if len(c.Clients) == 0 {
		return fmt.Errorf("at least one client must be configured")
	}

	keys := make(map[string]bool)
	for i, client := range c.Clients {
		if client.Name == "" {
			return fmt.Errorf("client %d: name is required", i)
		}
		if client.Key == "" {
			return fmt.Errorf("client %q: key is required", client.Name)
		}
		if keys[client.Key] {
			return fmt.Errorf("client %q: duplicate key", client.Name)
		}
		keys[client.Key] = true
	}
	return nil
}

// ClientByKey returns the client config for the given API key, or nil if not found.
func (c *Config) ClientByKey(key string) *Client {
	for i := range c.Clients {
		if c.Clients[i].Key == key {
			return &c.Clients[i]
		}
	}
	return nil
}

// ModelAllowed checks whether this client is permitted to use the given model.
// Denylist is checked first. Then allowlist must match (deny by default).
func (cl *Client) ModelAllowed(model string) bool {
	for _, pattern := range cl.DenyModels {
		if matchModel(pattern, model) {
			return false
		}
	}

	if len(cl.AllowModels) == 0 {
		return false
	}
	for _, pattern := range cl.AllowModels {
		if matchModel(pattern, model) {
			return true
		}
	}
	return false
}

// matchModel checks if a model name matches a pattern.
// "*" matches all models.
// A pattern without a tag (no ":") matches any tag of that model base name.
// Otherwise, exact match is required.
func matchModel(pattern, model string) bool {
	if pattern == "*" {
		return true
	}
	if pattern == model {
		return true
	}
	// Pattern without tag matches any tag of that base model
	if !strings.Contains(pattern, ":") {
		base, _, _ := strings.Cut(model, ":")
		return pattern == base
	}
	return false
}
