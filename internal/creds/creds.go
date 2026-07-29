// Package creds reads credentials.toml and materializes the secrets it points at.
package creds

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/dotnetemmanuel/blip/internal/output"
	"github.com/dotnetemmanuel/blip/internal/xdg"
)

// Kind is an auth mechanism.
type Kind string

const (
	KindNone     Kind = "none"
	KindBearer   Kind = "bearer"
	KindHeader   Kind = "header"
	KindBasic    Kind = "basic"
	KindOAuth2CC Kind = "oauth2_cc"
)

// RequiredMode is the only permission blip will read a credentials file at.
const RequiredMode os.FileMode = 0o600

// CommandTimeout bounds a secret-fetching command. Generous, because a vault may
// wait on a hardware key tap.
const CommandTimeout = 2 * time.Minute

// ErrNoFile means the credentials file does not exist.
var ErrNoFile = errors.New("no credentials file")

// Profile is one named block in credentials.toml.
type Profile struct {
	Type string `toml:"type"`

	Token        string `toml:"token"`
	TokenCommand string `toml:"token_command"`

	Header       string `toml:"header"`
	Value        string `toml:"value"`
	ValueCommand string `toml:"value_command"`

	Username        string `toml:"username"`
	Password        string `toml:"password"`
	PasswordCommand string `toml:"password_command"`

	TokenURL            string `toml:"token_url"`
	ClientID            string `toml:"client_id"`
	ClientSecret        string `toml:"client_secret"`
	ClientSecretCommand string `toml:"client_secret_command"`
	Scope               string `toml:"scope"`
	Audience            string `toml:"audience"`
}

// Store is a parsed credentials file.
type Store struct {
	Path     string
	Profiles map[string]Profile
}

// Resolved is a profile with every secret materialized. It must never be printed.
type Resolved struct {
	Name     string
	Kind     Kind
	Token    string
	Header   string
	Value    string
	Username string
	Password string

	TokenURL     string
	ClientID     string
	ClientSecret string
	Scope        string
	Audience     string
}

// SecretValues lists everything in this profile that must never reach output.
func (r *Resolved) SecretValues() []string {
	candidates := []string{r.Token, r.Value, r.Password, r.ClientSecret}
	secrets := make([]string, 0, len(candidates))
	for _, s := range candidates {
		if s != "" {
			secrets = append(secrets, s)
		}
	}
	return secrets
}

func authError(format string, args ...any) error {
	return output.WithCode(fmt.Errorf(format, args...), output.ExitAuth)
}

// Load reads the credentials file at the default location.
func Load() (*Store, error) {
	path, err := xdg.CredentialsPath()
	if err != nil {
		return nil, authError("locating credentials file: %w", err)
	}
	return LoadFile(path)
}

// LoadFile reads a credentials file, refusing anything more permissive than 0600.
func LoadFile(path string) (*Store, error) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, output.WithCode(fmt.Errorf("%w at %s", ErrNoFile, path), output.ExitAuth)
	case err != nil:
		return nil, authError("reading %s: %w", path, err)
	}

	if mode := info.Mode().Perm(); mode != RequiredMode {
		return nil, authError("%s has mode %#o but must be %#o; run: chmod 600 %s",
			path, mode, RequiredMode, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, authError("reading %s: %w", path, err)
	}

	profiles := map[string]Profile{}
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&profiles); err != nil {
		return nil, authError("%s: %w", path, err)
	}

	store := &Store{Path: path, Profiles: profiles}
	for _, name := range store.Names() {
		if err := validate(name, profiles[name], path); err != nil {
			return nil, err
		}
	}
	return store, nil
}

// Names lists the profiles in a stable order.
func (s *Store) Names() []string {
	names := make([]string, 0, len(s.Profiles))
	for name := range s.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validate(name string, p Profile, path string) error {
	kind := Kind(p.Type)
	switch kind {
	case KindNone:
	case KindBearer:
		if p.Token == "" && p.TokenCommand == "" {
			return authError("%s: profile %q needs token or token_command", path, name)
		}
	case KindHeader:
		if p.Header == "" {
			return authError("%s: profile %q needs header", path, name)
		}
		if p.Value == "" && p.ValueCommand == "" {
			return authError("%s: profile %q needs value or value_command", path, name)
		}
	case KindBasic:
		if p.Username == "" {
			return authError("%s: profile %q needs username", path, name)
		}
		if p.Password == "" && p.PasswordCommand == "" {
			return authError("%s: profile %q needs password or password_command", path, name)
		}
	case KindOAuth2CC:
		if p.TokenURL == "" {
			return authError("%s: profile %q needs token_url", path, name)
		}
		if p.ClientID == "" {
			return authError("%s: profile %q needs client_id", path, name)
		}
		if p.ClientSecret == "" && p.ClientSecretCommand == "" {
			return authError("%s: profile %q needs client_secret or client_secret_command", path, name)
		}
	case "":
		return authError("%s: profile %q has no type (one of none, bearer, header, basic, oauth2_cc)", path, name)
	default:
		return authError("%s: profile %q has unknown type %q", path, name, p.Type)
	}
	return nil
}

// Runner executes a secret-fetching command and returns its stdout.
type Runner func(ctx context.Context, command string) (string, error)

// Resolver materializes the secrets a profile points at.
type Resolver struct {
	Run Runner
}

// NewResolver returns a resolver that runs commands through the shell.
func NewResolver(stdin *os.File, stderr *os.File) *Resolver {
	return &Resolver{Run: shellRunner(stdin, stderr)}
}

// Resolve materializes every secret in the named profile.
func (s *Store) Resolve(ctx context.Context, name string, r *Resolver) (*Resolved, error) {
	p, ok := s.Profiles[name]
	if !ok {
		return nil, authError("profile %q is not in %s (have %s)", name, s.Path, strings.Join(s.Names(), ", "))
	}

	res := &Resolved{
		Name:     name,
		Kind:     Kind(p.Type),
		Header:   p.Header,
		TokenURL: p.TokenURL,
		Audience: p.Audience,
	}

	fields := []struct {
		label   string
		literal string
		command string
		dest    *string
	}{
		{"token", p.Token, p.TokenCommand, &res.Token},
		{"value", p.Value, p.ValueCommand, &res.Value},
		{"username", p.Username, "", &res.Username},
		{"password", p.Password, p.PasswordCommand, &res.Password},
		{"client_id", p.ClientID, "", &res.ClientID},
		{"client_secret", p.ClientSecret, p.ClientSecretCommand, &res.ClientSecret},
		{"scope", p.Scope, "", &res.Scope},
	}

	for _, f := range fields {
		v, err := r.resolveField(ctx, name, f.label, f.literal, f.command)
		if err != nil {
			return nil, err
		}
		*f.dest = v
	}

	return res, nil
}

func (r *Resolver) resolveField(ctx context.Context, profile, label, literal, command string) (string, error) {
	if literal != "" && command != "" {
		return "", authError("profile %q sets both %s and %s_command; pick one", profile, label, label)
	}
	if command != "" {
		if r == nil || r.Run == nil {
			return "", authError("profile %q needs %s_command but no command runner is configured", profile, label)
		}
		out, err := r.Run(ctx, command)
		if err != nil {
			return "", authError("profile %q: %s_command failed: %w", profile, label, err)
		}
		out = strings.TrimSpace(out)
		if out == "" {
			return "", authError("profile %q: %s_command produced no output", profile, label)
		}
		return out, nil
	}
	return expandEnv(profile, label, literal)
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func expandEnv(profile, label, value string) (string, error) {
	var missing []string
	expanded := envRef.ReplaceAllStringFunc(value, func(match string) string {
		name := match[2 : len(match)-1]
		v, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return ""
		}
		return v
	})
	if len(missing) > 0 {
		return "", authError("profile %q: %s references unset %s", profile, label, strings.Join(missing, ", "))
	}
	return expanded, nil
}

// shellRunner runs a command through sh so that pipes and quoting behave the way
// they do in the vault documentation users are copying from.
func shellRunner(stdin, stderr *os.File) Runner {
	return func(ctx context.Context, command string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, CommandTimeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		var stdout, captured bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &captured
		if stderr != nil {
			cmd.Stderr = io.MultiWriter(&captured, stderr)
		}
		cmd.Stdin = stdin

		if err := cmd.Run(); err != nil {
			if msg := strings.TrimSpace(captured.String()); msg != "" {
				return "", fmt.Errorf("%w: %s", err, firstLine(msg))
			}
			return "", err
		}
		return stdout.String(), nil
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
