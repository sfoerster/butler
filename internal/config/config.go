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

// Subject is a common identity type used by both Client and User for policy enforcement.
type Subject struct {
	Name         string
	AuthSource   string // "api_key" or "jwt"
	AllowModels  []string
	DenyModels   []string
	Rate         *RateSpec
	MaxReqBytes  int64
	MaxCtx       int
	MaxPredict   int
	DenyPatterns []*regexp.Regexp
}

// ModelAllowed checks whether this subject is permitted to use the given model.
func (s *Subject) ModelAllowed(model string) bool {
	for _, pattern := range s.DenyModels {
		if matchModel(pattern, model) {
			return false
		}
	}
	if len(s.AllowModels) == 0 {
		return false
	}
	for _, pattern := range s.AllowModels {
		if matchModel(pattern, model) {
			return true
		}
	}
	return false
}

// RateLimitKey returns a key for rate limiting that avoids collisions between auth sources.
func (s *Subject) RateLimitKey() string {
	return s.AuthSource + ":" + s.Name
}

// OIDCConfig defines OIDC federation settings.
type OIDCConfig struct {
	Issuer          string `yaml:"issuer"`
	ClientID        string `yaml:"client_id"`
	RoleClaimPath   string `yaml:"role_claim_path"`
	RefreshInterval string `yaml:"refresh_interval"`
	refreshInterval time.Duration
}

// RefreshIntervalDuration returns the parsed refresh interval, defaulting to 60m.
func (o *OIDCConfig) RefreshIntervalDuration() time.Duration {
	if o.refreshInterval == 0 {
		return 60 * time.Minute
	}
	return o.refreshInterval
}

// RolePolicy defines policy for a role (used with OIDC federation).
type RolePolicy struct {
	AllowModels        []string `yaml:"allow_models"`
	DenyModels         []string `yaml:"deny_models"`
	RateLimit          string   `yaml:"rate_limit"` // "120/hour", "unlimited", or ""
	MaxRequestBytes    int64    `yaml:"max_request_bytes"`
	MaxCtx             int      `yaml:"max_ctx"`
	MaxPredict         int      `yaml:"max_predict"`
	DenyPromptPatterns []string `yaml:"deny_prompt_patterns"`
	rate               *RateSpec
	denyPatterns       []*regexp.Regexp
	unlimited          bool
}

// AuthConfig defines authentication settings.
type AuthConfig struct {
	Mode        string      `yaml:"mode"` // "api_key", "jwt_standalone", "oidc", "either"
	JWTSecret   string      `yaml:"jwt_secret"`
	TokenExpiry string      `yaml:"token_expiry"` // e.g. "24h", default "24h"
	OIDC        *OIDCConfig `yaml:"oidc"`
	tokenExpiry time.Duration
}

// TokenExpiryDuration returns the parsed token expiry duration.
func (a *AuthConfig) TokenExpiryDuration() time.Duration {
	if a.tokenExpiry == 0 {
		return 24 * time.Hour
	}
	return a.tokenExpiry
}

// User defines a JWT-authenticated user.
type User struct {
	Name               string   `yaml:"name"`
	PasswordHash       string   `yaml:"password_hash"`
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

// Rate returns the parsed per-user rate limit, or nil if not set.
func (u *User) Rate() *RateSpec {
	return u.rate
}

// DenyPatterns returns the compiled deny prompt regexes.
func (u *User) DenyPatterns() []*regexp.Regexp {
	return u.denyPatterns
}

// Subject returns a Subject for this user.
func (u *User) Subject() *Subject {
	return &Subject{
		Name:         u.Name,
		AuthSource:   "jwt",
		AllowModels:  u.AllowModels,
		DenyModels:   u.DenyModels,
		Rate:         u.rate,
		MaxReqBytes:  u.MaxRequestBytes,
		MaxCtx:       u.MaxCtx,
		MaxPredict:   u.MaxPredict,
		DenyPatterns: u.denyPatterns,
	}
}

// SetRateForTest sets the parsed per-user rate (for testing without Load).
func (u *User) SetRateForTest(spec *RateSpec) {
	u.rate = spec
}

type Config struct {
	Listen          string                `yaml:"listen"`
	Upstream        string                `yaml:"upstream"`
	GlobalRateLimit string                `yaml:"global_rate_limit"`
	LogPrompts      bool                  `yaml:"log_prompts"`
	Auth            AuthConfig            `yaml:"auth"`
	Clients         []Client              `yaml:"clients"`
	Users           []User                `yaml:"users"`
	RolePolicies    map[string]RolePolicy `yaml:"role_policies"`
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

// Subject returns a Subject for this client.
func (cl *Client) Subject() *Subject {
	return &Subject{
		Name:         cl.Name,
		AuthSource:   "api_key",
		AllowModels:  cl.AllowModels,
		DenyModels:   cl.DenyModels,
		Rate:         cl.rate,
		MaxReqBytes:  cl.MaxRequestBytes,
		MaxCtx:       cl.MaxCtx,
		MaxPredict:   cl.MaxPredict,
		DenyPatterns: cl.denyPatterns,
	}
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

	// Auth mode defaults and validation
	if c.Auth.Mode == "" {
		c.Auth.Mode = "api_key"
	}

	switch c.Auth.Mode {
	case "api_key":
		if len(c.Clients) == 0 {
			return fmt.Errorf("at least one client must be configured")
		}
	case "jwt_standalone":
		if len(c.Users) == 0 {
			return fmt.Errorf("jwt_standalone mode requires at least one user")
		}
		if c.Auth.JWTSecret == "" {
			return fmt.Errorf("jwt_secret is required for %s mode", c.Auth.Mode)
		}
		if len(c.Auth.JWTSecret) < 32 {
			return fmt.Errorf("jwt_secret must be at least 32 characters")
		}
	case "oidc":
		if err := c.validateOIDC(); err != nil {
			return err
		}
	case "either":
		hasAPIKeys := len(c.Clients) > 0
		hasUsers := len(c.Users) > 0
		hasOIDC := c.Auth.OIDC != nil

		if !hasAPIKeys && !hasUsers && !hasOIDC {
			return fmt.Errorf("either mode requires at least one client, user, or oidc config")
		}
		if hasUsers || (hasAPIKeys && !hasOIDC) {
			// JWT secret needed when users are configured or when no OIDC fallback
			if hasUsers {
				if c.Auth.JWTSecret == "" {
					return fmt.Errorf("jwt_secret is required for %s mode", c.Auth.Mode)
				}
				if len(c.Auth.JWTSecret) < 32 {
					return fmt.Errorf("jwt_secret must be at least 32 characters")
				}
			}
		}
		// If OIDC is configured in either mode, validate it
		if hasOIDC {
			if err := c.validateOIDC(); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid auth mode %q: must be api_key, jwt_standalone, oidc, or either", c.Auth.Mode)
	}

	// Parse token expiry
	if c.Auth.TokenExpiry == "" {
		c.Auth.tokenExpiry = 24 * time.Hour
	} else {
		d, err := time.ParseDuration(c.Auth.TokenExpiry)
		if err != nil {
			return fmt.Errorf("token_expiry: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("token_expiry must be positive")
		}
		c.Auth.tokenExpiry = d
	}

	// Validate users
	userNames := make(map[string]bool)
	for i, user := range c.Users {
		if user.Name == "" {
			return fmt.Errorf("user %d: name is required", i)
		}
		if user.PasswordHash == "" {
			return fmt.Errorf("user %q: password_hash is required", user.Name)
		}
		if userNames[user.Name] {
			return fmt.Errorf("user %q: duplicate name", user.Name)
		}
		userNames[user.Name] = true

		if user.RateLimit != "" {
			spec, err := ParseRateLimit(user.RateLimit)
			if err != nil {
				return fmt.Errorf("user %q: rate_limit: %w", user.Name, err)
			}
			c.Users[i].rate = &spec
		}

		if user.MaxRequestBytes < 0 {
			return fmt.Errorf("user %q: max_request_bytes must not be negative", user.Name)
		}
		if user.MaxCtx < 0 {
			return fmt.Errorf("user %q: max_ctx must not be negative", user.Name)
		}
		if user.MaxPredict < 0 {
			return fmt.Errorf("user %q: max_predict must not be negative", user.Name)
		}

		for j, pattern := range user.DenyPromptPatterns {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return fmt.Errorf("user %q: deny_prompt_patterns[%d]: %w", user.Name, j, err)
			}
			c.Users[i].denyPatterns = append(c.Users[i].denyPatterns, re)
		}
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

// UserByName returns the user config for the given username, or nil if not found.
func (c *Config) UserByName(name string) *User {
	for i := range c.Users {
		if c.Users[i].Name == name {
			return &c.Users[i]
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

// SetRateForTest sets the parsed per-role rate (for testing without Load).
func (rp *RolePolicy) SetRateForTest(spec *RateSpec) {
	rp.rate = spec
}

// CompileUserPatternsForTest compiles DenyPromptPatterns for all users (for testing without Load).
func (c *Config) CompileUserPatternsForTest() {
	for i, user := range c.Users {
		for _, pattern := range user.DenyPromptPatterns {
			re := regexp.MustCompile(pattern)
			c.Users[i].denyPatterns = append(c.Users[i].denyPatterns, re)
		}
	}
}

func (c *Config) validateOIDC() error {
	if c.Auth.OIDC == nil {
		return fmt.Errorf("oidc config is required for %s mode", c.Auth.Mode)
	}
	oidc := c.Auth.OIDC
	if oidc.Issuer == "" {
		return fmt.Errorf("oidc.issuer is required")
	}
	issuerURL, err := url.Parse(oidc.Issuer)
	if err != nil {
		return fmt.Errorf("oidc.issuer must be a valid URL: %w", err)
	}
	if issuerURL.Scheme != "https" {
		return fmt.Errorf("oidc.issuer must use HTTPS")
	}
	if oidc.ClientID == "" {
		return fmt.Errorf("oidc.client_id is required")
	}
	if oidc.RoleClaimPath == "" {
		return fmt.Errorf("oidc.role_claim_path is required")
	}
	if oidc.RefreshInterval != "" {
		d, err := time.ParseDuration(oidc.RefreshInterval)
		if err != nil {
			return fmt.Errorf("oidc.refresh_interval: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("oidc.refresh_interval must be positive")
		}
		oidc.refreshInterval = d
	}

	if len(c.RolePolicies) == 0 {
		return fmt.Errorf("at least one role_policy is required for %s mode", c.Auth.Mode)
	}

	return c.validateRolePolicies()
}

func (c *Config) validateRolePolicies() error {
	for name, rp := range c.RolePolicies {
		if rp.RateLimit != "" {
			if rp.RateLimit == "unlimited" {
				rp.unlimited = true
			} else {
				spec, err := ParseRateLimit(rp.RateLimit)
				if err != nil {
					return fmt.Errorf("role_policy %q: rate_limit: %w", name, err)
				}
				rp.rate = &spec
			}
		}
		if rp.MaxRequestBytes < 0 {
			return fmt.Errorf("role_policy %q: max_request_bytes must not be negative", name)
		}
		if rp.MaxCtx < 0 {
			return fmt.Errorf("role_policy %q: max_ctx must not be negative", name)
		}
		if rp.MaxPredict < 0 {
			return fmt.Errorf("role_policy %q: max_predict must not be negative", name)
		}
		for j, pattern := range rp.DenyPromptPatterns {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return fmt.Errorf("role_policy %q: deny_prompt_patterns[%d]: %w", name, j, err)
			}
			rp.denyPatterns = append(rp.denyPatterns, re)
		}
		c.RolePolicies[name] = rp
	}
	return nil
}

// SubjectFromRoles merges matching role policies into a single Subject.
// Returns nil if no roles match any configured policy.
func (c *Config) SubjectFromRoles(name string, roles []string) *Subject {
	var matched []string
	for _, role := range roles {
		if _, ok := c.RolePolicies[role]; ok {
			matched = append(matched, role)
		}
	}
	if len(matched) == 0 {
		return nil
	}

	subj := &Subject{
		Name:       name,
		AuthSource: "oidc",
	}

	// Collect all allow models (union)
	allowSet := make(map[string]bool)
	hasWildcard := false
	var unlimitedRate bool

	for _, role := range matched {
		rp := c.RolePolicies[role]
		for _, m := range rp.AllowModels {
			if m == "*" {
				hasWildcard = true
			}
			allowSet[m] = true
		}

		// Rate: most permissive wins (unlimited > highest count)
		if rp.unlimited {
			unlimitedRate = true
		} else if rp.rate != nil && !unlimitedRate {
			if subj.Rate == nil {
				subj.Rate = rp.rate
			} else {
				// Compare rates: higher count or longer window is more permissive
				existingPerHour := float64(subj.Rate.Count) * float64(time.Hour) / float64(subj.Rate.Window)
				newPerHour := float64(rp.rate.Count) * float64(time.Hour) / float64(rp.rate.Window)
				if newPerHour > existingPerHour {
					subj.Rate = rp.rate
				}
			}
		}

		// MaxReqBytes: most permissive (0=no limit wins, otherwise highest)
		if rp.MaxRequestBytes == 0 && subj.MaxReqBytes != 0 {
			subj.MaxReqBytes = 0
		} else if rp.MaxRequestBytes > subj.MaxReqBytes {
			subj.MaxReqBytes = rp.MaxRequestBytes
		}

		// MaxCtx: most permissive
		if rp.MaxCtx == 0 && subj.MaxCtx != 0 {
			subj.MaxCtx = 0
		} else if rp.MaxCtx > subj.MaxCtx {
			subj.MaxCtx = rp.MaxCtx
		}

		// MaxPredict: most permissive
		if rp.MaxPredict == 0 && subj.MaxPredict != 0 {
			subj.MaxPredict = 0
		} else if rp.MaxPredict > subj.MaxPredict {
			subj.MaxPredict = rp.MaxPredict
		}
	}

	if unlimitedRate {
		subj.Rate = nil // nil means unlimited
	}

	if hasWildcard {
		subj.AllowModels = []string{"*"}
	} else {
		for m := range allowSet {
			subj.AllowModels = append(subj.AllowModels, m)
		}
	}

	// DenyModels/DenyPatterns: only applied when exactly one role matches
	if len(matched) == 1 {
		rp := c.RolePolicies[matched[0]]
		subj.DenyModels = rp.DenyModels
		subj.DenyPatterns = rp.denyPatterns
	}

	return subj
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
