// Package auth turns resolved credentials into request headers.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dotnetemmanuel/blip/internal/creds"
	"github.com/dotnetemmanuel/blip/internal/output"
	"github.com/dotnetemmanuel/blip/internal/xdg"
)

// RefreshWindow is how close to expiry a cached token may get before blip replaces it.
const RefreshWindow = 60 * time.Second

// Authenticator applies a credential to outgoing requests.
type Authenticator interface {
	// Apply sets whatever headers the mechanism needs.
	Apply(ctx context.Context, req *http.Request) error
	// Describe reports the mechanism and how the secret was obtained. Never secret.
	Describe(ctx context.Context) (string, error)
	// Secrets lists values that must be redacted from any output.
	Secrets() []string
	// PermitsHost reports whether this credential may be sent to a host.
	PermitsHost(host string) bool
	// Pinned reports whether the profile named the hosts it trusts.
	Pinned() bool
}

// binding carries the hosts a profile pinned itself to. An unpinned profile
// permits everything, which is why the caller warns about it.
type binding struct{ hosts []string }

func (b binding) Pinned() bool { return len(b.hosts) > 0 }

func (b binding) PermitsHost(host string) bool {
	if len(b.hosts) == 0 {
		return true
	}
	bare, _, err := net.SplitHostPort(host)
	if err != nil {
		bare = host
	}
	for _, allowed := range b.hosts {
		if strings.EqualFold(allowed, host) || strings.EqualFold(allowed, bare) {
			return true
		}
	}
	return false
}

func authError(format string, args ...any) error {
	return output.Authf(format, args...)
}

// New builds an authenticator for a resolved profile. client is used only by
// oauth2_cc, which has to make a request of its own.
func New(r *creds.Resolved, client *http.Client) (Authenticator, error) {
	switch r.Kind {
	case creds.KindNone:
		return none{}, nil
	case creds.KindBearer:
		return bearer{binding: binding{r.Hosts}, token: r.Token}, nil
	case creds.KindHeader:
		return headerAuth{binding: binding{r.Hosts}, name: r.Header, value: r.Value}, nil
	case creds.KindBasic:
		return basic{binding: binding{r.Hosts}, user: r.Username, password: r.Password}, nil
	case creds.KindOAuth2CC:
		return &oauth2CC{binding: binding{r.Hosts}, profile: r.Name, resolved: r, client: client}, nil
	default:
		return nil, authError("profile %q has unsupported type %q", r.Name, r.Kind)
	}
}

// None is the authenticator for environments with no auth configured.
func None() Authenticator { return none{} }

type none struct{}

func (none) Pinned() bool                               { return true }
func (none) PermitsHost(string) bool                    { return true }
func (none) Apply(context.Context, *http.Request) error { return nil }
func (none) Describe(context.Context) (string, error)   { return "none", nil }
func (none) Secrets() []string                          { return nil }

type bearer struct {
	binding
	token string
}

func (b bearer) Apply(_ context.Context, req *http.Request) error {
	req.Header.Set("Authorization", "Bearer "+b.token)
	return nil
}

func (b bearer) Describe(context.Context) (string, error) {
	return "bearer token " + Fingerprint(b.token), nil
}

func (b bearer) Secrets() []string { return []string{b.token} }

type headerAuth struct {
	binding
	name, value string
}

func (h headerAuth) Apply(_ context.Context, req *http.Request) error {
	req.Header.Set(h.name, h.value)
	return nil
}

func (h headerAuth) Describe(context.Context) (string, error) {
	return fmt.Sprintf("header %s %s", h.name, Fingerprint(h.value)), nil
}

func (h headerAuth) Secrets() []string { return []string{h.value} }

type basic struct {
	binding
	user, password string
}

func (b basic) Apply(_ context.Context, req *http.Request) error {
	req.SetBasicAuth(b.user, b.password)
	return nil
}

func (b basic) Describe(context.Context) (string, error) {
	return fmt.Sprintf("basic %s %s", b.user, Fingerprint(b.password)), nil
}

// The encoded pair is as sensitive as the password itself.
func (b basic) Secrets() []string {
	encoded := base64.StdEncoding.EncodeToString([]byte(b.user + ":" + b.password))
	return []string{b.password, encoded}
}

// Fingerprint identifies a secret without revealing it, so two runs can be compared.
func Fingerprint(secret string) string {
	if secret == "" {
		return "(empty)"
	}
	sum := sha256.Sum256([]byte(secret))
	return "sha256:" + hex.EncodeToString(sum[:])[:8]
}

type oauth2CC struct {
	binding
	profile  string
	resolved *creds.Resolved
	client   *http.Client
	token    string
}

func (o *oauth2CC) Apply(ctx context.Context, req *http.Request) error {
	token, err := o.accessToken(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

func (o *oauth2CC) Describe(ctx context.Context) (string, error) {
	token, err := o.accessToken(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("oauth2_cc client %s token %s", o.resolved.ClientID, Fingerprint(token)), nil
}

func (o *oauth2CC) Secrets() []string {
	secrets := []string{o.resolved.ClientSecret}
	if o.token != "" {
		secrets = append(secrets, o.token)
	}
	return secrets
}

func (o *oauth2CC) accessToken(ctx context.Context) (string, error) {
	if o.token != "" {
		return o.token, nil
	}

	path, err := o.cachePath()
	if err != nil {
		return "", err
	}
	if tok, ok := readCachedToken(path); ok {
		o.token = tok
		return tok, nil
	}

	tok, expiry, err := o.fetch(ctx)
	if err != nil {
		return "", err
	}
	writeCachedToken(path, tok, expiry)
	o.token = tok
	return tok, nil
}

func (o *oauth2CC) fetch(ctx context.Context) (string, time.Time, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {o.resolved.ClientID},
		"client_secret": {o.resolved.ClientSecret},
	}
	if o.resolved.Scope != "" {
		form.Set("scope", o.resolved.Scope)
	}
	if o.resolved.Audience != "" {
		form.Set("audience", o.resolved.Audience)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.resolved.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, authError("profile %q: building token request: %w", o.profile, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := o.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", time.Time{}, authError("profile %q: token request to %s failed: %w", o.profile, o.resolved.TokenURL, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, authError("profile %q: token endpoint returned %d: %s",
			o.profile, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", time.Time{}, authError("profile %q: token endpoint returned unparseable JSON: %w", o.profile, err)
	}
	if payload.AccessToken == "" {
		return "", time.Time{}, authError("profile %q: token endpoint returned no access_token", o.profile)
	}

	expiry := time.Time{}
	if payload.ExpiresIn > 0 {
		expiry = time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second)
	}
	return payload.AccessToken, expiry, nil
}

var unsafeInFilename = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// cachePath keys on the profile plus the parameters that decide what the token is
// for, so widening a scope cannot be served from a narrower cached token.
func (o *oauth2CC) cachePath() (string, error) {
	material := strings.Join([]string{o.resolved.TokenURL, o.resolved.ClientID, o.resolved.Scope, o.resolved.Audience}, "|")
	sum := sha256.Sum256([]byte(material))
	key := unsafeInFilename.ReplaceAllString(o.profile, "_") + "-" + hex.EncodeToString(sum[:])[:8]

	path, err := xdg.TokenCachePath(key)
	if err != nil {
		return "", authError("locating token cache: %w", err)
	}
	return path, nil
}

type tokenFile struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func readCachedToken(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var tok tokenFile
	if err := json.Unmarshal(data, &tok); err != nil || tok.AccessToken == "" {
		return "", false
	}
	if !tok.ExpiresAt.IsZero() && time.Until(tok.ExpiresAt) <= RefreshWindow {
		return "", false
	}
	return tok.AccessToken, true
}

// writeCachedToken is best effort: a cache that cannot be written costs a round
// trip next time, which is not worth failing the request over.
func writeCachedToken(path, token string, expiry time.Time) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(tokenFile{AccessToken: token, ExpiresAt: expiry})
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
