package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotnetemmanuel/blip/internal/creds"
	"github.com/dotnetemmanuel/blip/internal/output"
)

func request(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "https://api.test/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestApply(t *testing.T) {
	tests := []struct {
		name     string
		resolved *creds.Resolved
		header   string
		want     string
	}{
		{
			name:     "bearer",
			resolved: &creds.Resolved{Kind: creds.KindBearer, Token: "tok"},
			header:   "Authorization",
			want:     "Bearer tok",
		},
		{
			name:     "custom header",
			resolved: &creds.Resolved{Kind: creds.KindHeader, Header: "X-Api-Key", Value: "k"},
			header:   "X-Api-Key",
			want:     "k",
		},
		{
			name:     "basic",
			resolved: &creds.Resolved{Kind: creds.KindBasic, Username: "u", Password: "p"},
			header:   "Authorization",
			want:     "Basic " + base64.StdEncoding.EncodeToString([]byte("u:p")),
		},
		{
			name:     "none",
			resolved: &creds.Resolved{Kind: creds.KindNone},
			header:   "Authorization",
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := New(tt.resolved, nil)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			req := request(t)
			if err := a.Apply(context.Background(), req); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got := req.Header.Get(tt.header); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestDescribeRevealsNoSecret(t *testing.T) {
	profiles := []*creds.Resolved{
		{Kind: creds.KindBearer, Token: "super-secret-token"},
		{Kind: creds.KindHeader, Header: "X-Api-Key", Value: "super-secret-token"},
		{Kind: creds.KindBasic, Username: "u", Password: "super-secret-token"},
	}

	for _, p := range profiles {
		a, err := New(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		desc, err := a.Describe(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(desc, "super-secret-token") {
			t.Errorf("Describe = %q, want no secret in it", desc)
		}
		if !strings.Contains(desc, "sha256:") {
			t.Errorf("Describe = %q, want a fingerprint", desc)
		}
	}
}

func TestBasicSecretsIncludeTheEncodedPair(t *testing.T) {
	a, err := New(&creds.Resolved{Kind: creds.KindBasic, Username: "u", Password: "p"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	encoded := base64.StdEncoding.EncodeToString([]byte("u:p"))
	found := false
	for _, s := range a.Secrets() {
		if s == encoded {
			found = true
		}
	}
	if !found {
		t.Errorf("Secrets = %v, want the base64 pair so redaction can catch the header", a.Secrets())
	}
}

func TestFingerprintIsStableAndShort(t *testing.T) {
	if Fingerprint("abc") != Fingerprint("abc") {
		t.Error("Fingerprint is not stable")
	}
	if Fingerprint("abc") == Fingerprint("abd") {
		t.Error("Fingerprint collides on different secrets")
	}
	if got := Fingerprint(""); got != "(empty)" {
		t.Errorf("Fingerprint(\"\") = %q, want (empty)", got)
	}
}

func oauthServer(t *testing.T, calls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if got := r.PostForm.Get("grant_type"); got != "client_credentials" {
			t.Errorf("grant_type = %q, want client_credentials", got)
		}
		if got := r.PostForm.Get("client_secret"); got != "shh" {
			t.Errorf("client_secret = %q, want shh", got)
		}
		if got := r.PostForm.Get("scope"); got != "orders.read" {
			t.Errorf("scope = %q, want orders.read", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1", "expires_in": 3600})
	}))
}

func oauthProfile(tokenURL string) *creds.Resolved {
	return &creds.Resolved{
		Name:         "svc",
		Kind:         creds.KindOAuth2CC,
		TokenURL:     tokenURL,
		ClientID:     "blip",
		ClientSecret: "shh",
		Scope:        "orders.read",
	}
}

func TestOAuth2ClientCredentials(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	calls := 0
	srv := oauthServer(t, &calls)
	defer srv.Close()

	a, err := New(oauthProfile(srv.URL), srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	req := request(t)
	if err := a.Apply(context.Background(), req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer at-1" {
		t.Errorf("Authorization = %q, want Bearer at-1", got)
	}
	if calls != 1 {
		t.Errorf("token endpoint calls = %d, want 1", calls)
	}

	// A second authenticator must reuse the on-disk token rather than refetch.
	b, err := New(oauthProfile(srv.URL), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Apply(context.Background(), request(t)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if calls != 1 {
		t.Errorf("token endpoint calls = %d, want the cached token to be reused", calls)
	}
}

func TestOAuth2TokenCacheIsPrivate(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	calls := 0
	srv := oauthServer(t, &calls)
	defer srv.Close()

	a, _ := New(oauthProfile(srv.URL), srv.Client())
	if err := a.Apply(context.Background(), request(t)); err != nil {
		t.Fatal(err)
	}

	matches, err := filepath.Glob(filepath.Join(cache, "blip", "tokens", "*.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("token cache files = %v (err %v), want exactly one", matches, err)
	}
	info, err := os.Stat(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("token cache mode = %#o, want 0600", got)
	}
}

func TestOAuth2IgnoresATokenAboutToExpire(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	calls := 0
	srv := oauthServer(t, &calls)
	defer srv.Close()

	a, _ := New(oauthProfile(srv.URL), srv.Client())
	if err := a.Apply(context.Background(), request(t)); err != nil {
		t.Fatal(err)
	}

	matches, _ := filepath.Glob(filepath.Join(cache, "blip", "tokens", "*.json"))
	stale, _ := json.Marshal(tokenFile{AccessToken: "at-stale", ExpiresAt: time.Now().Add(RefreshWindow / 2)})
	if err := os.WriteFile(matches[0], stale, 0o600); err != nil {
		t.Fatal(err)
	}

	b, _ := New(oauthProfile(srv.URL), srv.Client())
	req := request(t)
	if err := b.Apply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer at-1" {
		t.Errorf("Authorization = %q, want a freshly fetched token", got)
	}
	if calls != 2 {
		t.Errorf("token endpoint calls = %d, want a refetch inside the refresh window", calls)
	}
}

func TestOAuth2ScopeChangeUsesADifferentCacheEntry(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "expires_in": 3600})
	}))
	defer srv.Close()

	first := oauthProfile(srv.URL)
	a, _ := New(first, srv.Client())
	if err := a.Apply(context.Background(), request(t)); err != nil {
		t.Fatal(err)
	}

	widened := oauthProfile(srv.URL)
	widened.Scope = "orders.read orders.write"
	b, _ := New(widened, srv.Client())
	if err := b.Apply(context.Background(), request(t)); err != nil {
		t.Fatal(err)
	}

	if calls != 2 {
		t.Errorf("token endpoint calls = %d, want a widened scope to bypass the cache", calls)
	}
}

func TestOAuth2Failures(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{
			name: "non-200",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			},
			want: "invalid_client",
		},
		{
			name: "no access_token",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{}`))
			},
			want: "no access_token",
		},
		{
			name: "not JSON",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`<html>nope</html>`))
			},
			want: "unparseable JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			a, _ := New(oauthProfile(srv.URL), srv.Client())
			err := a.Apply(context.Background(), request(t))
			if err == nil {
				t.Fatal("Apply succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %q, want it to mention %q", err, tt.want)
			}
			if got := output.ExitCodeFor(err); got != output.ExitAuth {
				t.Errorf("exit code = %d, want %d", got, output.ExitAuth)
			}
		})
	}
}
