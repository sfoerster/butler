package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// RateSpec holds a parsed rate limit specification.
type RateSpec struct {
	Count  int
	Window time.Duration
}

// ParseRateLimit parses a rate limit string like "600/min" or "100/hour".
func ParseRateLimit(s string) (RateSpec, error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return RateSpec{}, fmt.Errorf("invalid rate limit %q: expected format <count>/<unit>", s)
	}
	count, err := strconv.Atoi(parts[0])
	if err != nil || count <= 0 {
		return RateSpec{}, fmt.Errorf("invalid rate limit %q: count must be a positive integer", s)
	}
	var window time.Duration
	switch parts[1] {
	case "min":
		window = time.Minute
	case "hour":
		window = time.Hour
	default:
		return RateSpec{}, fmt.Errorf("invalid rate limit %q: unit must be min or hour", s)
	}
	return RateSpec{Count: count, Window: window}, nil
}

type Config struct {
	Listen          string   `yaml:"listen"`
	Upstream        string   `yaml:"upstream"`
	GlobalRateLimit string   `yaml:"global_rate_limit"`
	LogPrompts      bool     `yaml:"log_prompts"`
	Clients         []Client `yaml:"clients"`
	globalRate      *RateSpec
}

// GlobalRate returns the parsed global rate limit, or nil if not set.
func (c *Config) GlobalRate() *RateSpec {
	return c.globalRate
}

type Client struct {
	Name               string   `yaml:"name"`
	Key                string   `yaml:"key"`
	AllowModels        []string `yaml:"allow_models"`
	DenyModels         []string `yaml:"deny_models"`
	RateLimit          string   `yaml:"rate_limit"`
	MaxRequestBytes    int64    `yaml:"max_request_bytes"`
	MaxCtx             int      `yaml:"max_ctx"`
	MaxPredict         int      `yaml:"max_predict"`
	DenyPromptPatterns []string `yaml:"deny_prompt_patterns"`
	rate               *RateSpec
	denyPatterns       []*regexp.Regexp
}

// Rate returns the parsed per-client rate limit, or nil if not set.
func (cl *Client) Rate() *RateSpec {
	return cl.rate
}

// DenyPatterns returns the compiled deny prompt regexes.
func (cl *Client) DenyPatterns() []*regexp.Regexp {
	return cl.denyPatterns
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
	upstreamURL, err := url.Parse(c.Upstream)
	if err != nil {
		return fmt.Errorf("upstream must be a valid URL: %w", err)
	}
	if upstreamURL.Scheme == "" || upstreamURL.Host == "" {
		return fmt.Errorf("upstream must include scheme and host")
	}
	if upstreamURL.Scheme != "http" && upstreamURL.Scheme != "https" {
		return fmt.Errorf("upstream scheme must be http or https")
	}

	// Parse global rate limit
	if c.GlobalRateLimit != "" {
		spec, err := ParseRateLimit(c.GlobalRateLimit)
		if err != nil {
			return fmt.Errorf("global_rate_limit: %w", err)
		}
		c.globalRate = &spec
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

		// Parse per-client rate limit
		if client.RateLimit != "" {
			spec, err := ParseRateLimit(client.RateLimit)
			if err != nil {
				return fmt.Errorf("client %q: rate_limit: %w", client.Name, err)
			}
			c.Clients[i].rate = &spec
		}

		// Validate numeric caps
		if client.MaxRequestBytes < 0 {
			return fmt.Errorf("client %q: max_request_bytes must not be negative", client.Name)
		}
		if client.MaxCtx < 0 {
			return fmt.Errorf("client %q: max_ctx must not be negative", client.Name)
		}
		if client.MaxPredict < 0 {
			return fmt.Errorf("client %q: max_predict must not be negative", client.Name)
		}

		// Compile deny prompt patterns
		for j, pattern := range client.DenyPromptPatterns {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return fmt.Errorf("client %q: deny_prompt_patterns[%d]: %w", client.Name, j, err)
			}
			c.Clients[i].denyPatterns = append(c.Clients[i].denyPatterns, re)
		}
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

// SetGlobalRateForTest sets the parsed global rate (for testing without Load).
func (c *Config) SetGlobalRateForTest(spec *RateSpec) {
	c.globalRate = spec
}

// SetRateForTest sets the parsed per-client rate (for testing without Load).
func (cl *Client) SetRateForTest(spec *RateSpec) {
	cl.rate = spec
}

// CompilePatternsForTest compiles DenyPromptPatterns for all clients (for testing without Load).
func (c *Config) CompilePatternsForTest() {
	for i, client := range c.Clients {
		for _, pattern := range client.DenyPromptPatterns {
			re := regexp.MustCompile(pattern)
			c.Clients[i].denyPatterns = append(c.Clients[i].denyPatterns, re)
		}
	}
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
