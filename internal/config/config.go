// Package config discovers and resolves .blip.toml.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/dotnetemmanuel/blip/internal/output"
)

// Names blip looks for, in order, at each level of the walk up from cwd.
var Names = []string{".blip.toml", "blip.toml"}

// DefaultTimeout applies when an environment does not set one.
const DefaultTimeout = 30 * time.Second

// ErrNotFound means no config file exists between cwd and the filesystem root.
var ErrNotFound = errors.New("no .blip.toml found in this directory or any parent")

// Route is a hand-declared endpoint, for APIs with no spec.
type Route struct {
	Name    string `toml:"name"`
	Method  string `toml:"method"`
	Path    string `toml:"path"`
	Summary string `toml:"summary"`
	Group   string `toml:"group"`
}

// Env is one environment block as written in the file.
type Env struct {
	BaseURL    string `toml:"base_url"`
	SpecURL    string `toml:"spec_url"`
	Insecure   bool   `toml:"insecure"`
	Auth       string `toml:"auth"`
	Timeout    string `toml:"timeout"`
	Readonly   bool   `toml:"readonly"`
	ClientCert string `toml:"client_cert"`
	ClientKey  string `toml:"client_key"`
}

// File mirrors the TOML document.
type File struct {
	Name       string         `toml:"name"`
	DefaultEnv string         `toml:"default_env"`
	Envs       map[string]Env `toml:"env"`
	Routes     []Route        `toml:"route"`
}

// Config is a parsed file plus where it was found.
type Config struct {
	File
	Path string
	Root string
}

// Environment is a validated environment, ready to make requests against.
type Environment struct {
	Name       string
	BaseURL    *url.URL
	SpecURL    string
	Insecure   bool
	Auth       string
	Timeout    time.Duration
	Readonly   bool
	ClientCert string
	ClientKey  string
}

func configError(format string, args ...any) error {
	return output.WithCode(fmt.Errorf(format, args...), output.ExitConfig)
}

// Discover walks up from start looking for a config file. The first hit wins.
func Discover(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", configError("resolving %s: %w", start, err)
	}
	for {
		for _, name := range Names {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", output.WithCode(ErrNotFound, output.ExitConfig)
		}
		dir = parent
	}
}

// Load reads and validates a config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, configError("reading %s: %w", path, err)
	}

	var file File
	dec := toml.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, configError("%s: %s", path, decodeMessage(err))
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, configError("resolving %s: %w", path, err)
	}

	cfg := &Config{File: file, Path: abs, Root: filepath.Dir(abs)}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// decodeMessage turns a go-toml error into something that names the offending key,
// which the plain Error() text does not.
func decodeMessage(err error) string {
	var strict *toml.StrictMissingError
	if errors.As(err, &strict) {
		return "unknown key\n" + strict.String()
	}
	var decode *toml.DecodeError
	if errors.As(err, &decode) {
		return decode.Error() + "\n" + decode.String()
	}
	return err.Error()
}

// LoadFrom discovers a config starting at dir and loads it.
func LoadFrom(dir string) (*Config, error) {
	path, err := Discover(dir)
	if err != nil {
		return nil, err
	}
	return Load(path)
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return configError("%s: name is required; it is the cache key and display name", c.Path)
	}
	if len(c.Envs) == 0 {
		return configError("%s: no environments defined; add an [env.<name>] block", c.Path)
	}
	for _, name := range c.EnvNames() {
		if _, err := c.buildEnvironment(name); err != nil {
			return err
		}
	}
	if c.DefaultEnv != "" {
		if _, ok := c.Envs[c.DefaultEnv]; !ok {
			return configError("%s: default_env %q is not a defined environment (have %s)",
				c.Path, c.DefaultEnv, strings.Join(c.EnvNames(), ", "))
		}
	}
	return c.validateRoutes()
}

func (c *Config) validateRoutes() error {
	seen := make(map[string]bool, len(c.Routes))
	for i, r := range c.Routes {
		switch {
		case strings.TrimSpace(r.Name) == "":
			return configError("%s: [[route]] %d has no name", c.Path, i+1)
		case strings.TrimSpace(r.Method) == "":
			return configError("%s: route %q has no method", c.Path, r.Name)
		case !strings.HasPrefix(r.Path, "/"):
			return configError("%s: route %q path must start with /", c.Path, r.Name)
		case seen[r.Name]:
			return configError("%s: route %q is declared twice", c.Path, r.Name)
		}
		seen[r.Name] = true
	}
	return nil
}

// EnvNames lists the defined environments in a stable order.
func (c *Config) EnvNames() []string {
	names := make([]string, 0, len(c.Envs))
	for name := range c.Envs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ResolveEnv picks an environment: explicit flag, then BLIP_ENV, then default_env,
// then the sole environment if there is exactly one.
func (c *Config) ResolveEnv(explicit string) (*Environment, error) {
	name := explicit
	source := "--env"

	if name == "" {
		name, source = os.Getenv("BLIP_ENV"), "BLIP_ENV"
	}
	if name == "" {
		name, source = c.DefaultEnv, "default_env"
	}
	if name == "" && len(c.Envs) == 1 {
		name, source = c.EnvNames()[0], "the only environment"
	}
	if name == "" {
		return nil, configError("%s: no environment selected; set default_env or pass --env (have %s)",
			c.Path, strings.Join(c.EnvNames(), ", "))
	}
	if _, ok := c.Envs[name]; !ok {
		return nil, configError("environment %q (from %s) is not defined in %s (have %s)",
			name, source, c.Path, strings.Join(c.EnvNames(), ", "))
	}
	return c.buildEnvironment(name)
}

func (c *Config) buildEnvironment(name string) (*Environment, error) {
	raw, ok := c.Envs[name]
	if !ok {
		return nil, configError("environment %q is not defined in %s", name, c.Path)
	}

	base, err := parseBaseURL(name, raw.BaseURL, c.Path)
	if err != nil {
		return nil, err
	}

	timeout := DefaultTimeout
	if raw.Timeout != "" {
		timeout, err = time.ParseDuration(raw.Timeout)
		if err != nil {
			return nil, configError("%s: env.%s.timeout %q is not a duration (try \"30s\")", c.Path, name, raw.Timeout)
		}
		if timeout <= 0 {
			return nil, configError("%s: env.%s.timeout must be positive", c.Path, name)
		}
	}

	if raw.Insecure && !isLocalHost(base.Hostname()) {
		return nil, configError("%s: env.%s has insecure = true but base_url host %q is not local; "+
			"TLS verification can only be skipped against localhost, 127.0.0.1 or ::1",
			c.Path, name, base.Hostname())
	}

	if (raw.ClientCert == "") != (raw.ClientKey == "") {
		return nil, configError("%s: env.%s needs both client_cert and client_key, or neither", c.Path, name)
	}

	return &Environment{
		Name:       name,
		BaseURL:    base,
		SpecURL:    raw.SpecURL,
		Insecure:   raw.Insecure,
		Auth:       raw.Auth,
		Timeout:    timeout,
		Readonly:   raw.Readonly,
		ClientCert: c.resolvePath(raw.ClientCert),
		ClientKey:  c.resolvePath(raw.ClientKey),
	}, nil
}

// resolvePath makes a relative path in the config relative to the config file itself,
// not to wherever the caller happens to be standing.
func (c *Config) resolvePath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.Root, p)
}

func parseBaseURL(envName, raw, configPath string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, configError("%s: env.%s.base_url is required", configPath, envName)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, configError("%s: env.%s.base_url %q is not a URL: %w", configPath, envName, raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, configError("%s: env.%s.base_url must be http or https, got %q", configPath, envName, raw)
	}
	if u.Host == "" {
		return nil, configError("%s: env.%s.base_url %q has no host", configPath, envName, raw)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u, nil
}

func isLocalHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	// A literal in the loopback range is as local as the three named hosts.
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
