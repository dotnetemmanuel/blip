// Package spec fetches, caches and revalidates an OpenAPI document.
package spec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dotnetemmanuel/blip/internal/config"
	"github.com/dotnetemmanuel/blip/internal/output"
	"github.com/dotnetemmanuel/blip/internal/xdg"
)

// ProbePaths are tried in order when an environment does not name a spec_url.
// The first two cover .NET minimal APIs, from Microsoft.AspNetCore.OpenApi and
// from Swashbuckle respectively.
var ProbePaths = []string{
	"/openapi/v1.json",
	"/swagger/v1/swagger.json",
	"/openapi/v1.yaml",
	"/swagger/v1/swagger.yaml",
}

// NegativeTTL is how long blip remembers that an environment serves no spec.
// Without it, a config that declares routes instead of a spec would pay for four
// failed probes on every single command.
const NegativeTTL = 10 * time.Minute

// How a spec came to be in hand, which is worth reporting under --verbose.
// The distinction that matters is whether the document changed, not whether a
// full GET happened: a server with no ETag makes every run a full GET.
const (
	StatusFetched     = "fetched"     // the document is new or has changed
	StatusRevalidated = "revalidated" // confirmed unchanged, by 304 or by bytes
	StatusCached      = "cached"      // served from disk, network not consulted
	StatusStale       = "stale"       // the backend was unreachable
)

// Spec is a document plus where it came from.
type Spec struct {
	Data   []byte
	Meta   Meta
	Path   string
	Status string
}

// Meta is what blip remembers about a cached spec between runs.
type Meta struct {
	URL          string    `json:"url"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	FetchedAt    time.Time `json:"fetched_at"`
}

// Fetcher loads a spec, using the cache and the network according to the flags.
type Fetcher struct {
	Client  *http.Client
	Refresh bool
	Offline bool
	Warnf   func(format string, args ...any)

	// Authorize applies credentials to a spec request. It is called only after an
	// unauthenticated attempt has been refused, so a public spec never reaches for
	// a vault.
	Authorize func(ctx context.Context, req *http.Request) error
}

func configError(format string, args ...any) error {
	return output.Configf(format, args...)
}

func (f *Fetcher) warn(format string, args ...any) {
	if f.Warnf != nil {
		f.Warnf(format, args...)
	}
}

// CacheDir is where the spec for one environment of one API is kept. The
// environment is part of the key because dev and prod can serve different specs.
func CacheDir(apiName, envName string) (string, error) {
	dir, err := xdg.SpecCacheDir(apiName)
	if err != nil {
		return "", configError("locating spec cache: %w", err)
	}
	return filepath.Join(dir, envName), nil
}

// Load returns the spec for an environment, from cache or from the network.
func (f *Fetcher) Load(ctx context.Context, apiName string, env *config.Environment) (*Spec, error) {
	dir, err := CacheDir(apiName, env.Name)
	if err != nil {
		return nil, err
	}
	cached, cachedMeta := readCache(dir)

	if f.Offline {
		if cached == nil {
			return nil, configError("--offline was given but there is no cached spec for %s/%s at %s",
				apiName, env.Name, dir)
		}
		return &Spec{Data: cached, Meta: cachedMeta, Path: specPath(dir), Status: StatusCached}, nil
	}

	if env.SpecURL == "" && !f.Refresh && recentlyProbedInVain(dir) {
		return nil, configError("no OpenAPI spec was found for env.%s the last time blip looked, and nothing has been re-probed since; run with --refresh to look again, or set spec_url",
			env.Name)
	}

	candidates, err := f.candidates(env, cachedMeta)
	if err != nil {
		return nil, err
	}

	var reachable, unreachable []string
	transient := false
	for i, candidate := range candidates {
		etag := ""
		if !f.Refresh && candidate == cachedMeta.URL {
			etag = cachedMeta.ETag
		}

		status, body, meta, err := f.get(ctx, candidate, etag, false)
		if isUnauthorized(status) && f.Authorize != nil {
			if !sameHost(candidate, env.BaseURL) {
				return nil, output.Blockedf(
					"refusing to send credentials to %s: env.%s.spec_url points at a different host from base_url (%s)",
					candidate, env.Name, env.BaseURL.Host)
			}
			status, body, meta, err = f.get(ctx, candidate, etag, true)
			if err != nil {
				// A credential that cannot be produced is an auth problem, not an
				// unreachable host, and the code it carries has to survive.
				return nil, err
			}
			if isUnauthorized(status) {
				return nil, output.Authf(
					"%s returned %d even with the credentials for env.%s", candidate, status, env.Name)
			}
		}
		switch {
		case err != nil:
			unreachable = append(unreachable, fmt.Sprintf("%s (%v)", candidate, err))
			continue

		case status == http.StatusNotModified && cached != nil:
			merged := mergeMeta(cachedMeta, meta)
			if err := writeCache(dir, cached, merged); err != nil {
				f.warn("%v", err)
			}
			return &Spec{Data: cached, Meta: merged, Path: specPath(dir), Status: StatusRevalidated}, nil

		case status == http.StatusOK:
			if !looksLikeSpec(body) {
				reachable = append(reachable, fmt.Sprintf("%s (200 but not an OpenAPI document)", candidate))
				continue
			}
			if err := writeCache(dir, body, meta); err != nil {
				f.warn("%v", err)
			}
			_ = os.Remove(noSpecMarker(dir))
			if i > 0 && cachedMeta.URL != "" && candidate != cachedMeta.URL {
				f.warn("spec moved to %s", candidate)
			}
			// A server with no ETag makes every run a full fetch; only the bytes
			// can say whether anything actually changed.
			outcome := StatusFetched
			if bytes.Equal(body, cached) {
				outcome = StatusRevalidated
			}
			return &Spec{Data: body, Meta: meta, Path: specPath(dir), Status: outcome}, nil

		default:
			reachable = append(reachable, fmt.Sprintf("%s (%d)", candidate, status))
			if !definitelyAbsent(status) {
				transient = true
			}
		}
	}

	// A backend being down is exactly when this tool is most useful, so a warm
	// cache beats an error.
	if cached != nil {
		f.warn("could not refresh the spec, using the cache from %s", cachedMeta.FetchedAt.Format(time.RFC3339))
		return &Spec{Data: cached, Meta: cachedMeta, Path: specPath(dir), Status: StatusStale}, nil
	}

	if len(reachable) > 0 && !transient {
		rememberNoSpec(dir)
	}
	return nil, f.noSpecError(env, reachable, unreachable)
}

// definitelyAbsent distinguishes "this service serves no spec here" from "this
// service is not ready yet". Only the first is worth remembering.
func definitelyAbsent(status int) bool {
	return status == http.StatusNotFound || status == http.StatusGone
}

func noSpecMarker(dir string) string { return filepath.Join(dir, "nospec") }

// recentlyProbedInVain reports whether blip already probed this environment and
// came away empty, recently enough to trust.
func recentlyProbedInVain(dir string) bool {
	info, err := os.Stat(noSpecMarker(dir))
	return err == nil && time.Since(info.ModTime()) < NegativeTTL
}

func rememberNoSpec(dir string) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(noSpecMarker(dir), nil, 0o600)
}

func (f *Fetcher) noSpecError(env *config.Environment, reachable, unreachable []string) error {
	if len(reachable) == 0 && len(unreachable) > 0 {
		return output.WithCode(fmt.Errorf("could not reach %s to load a spec: %s",
			env.BaseURL, strings.Join(unreachable, "; ")), output.ExitTransport)
	}
	return configError("no OpenAPI spec found for env.%s; tried %s. Set spec_url, or declare [[route]] entries and use them without a spec",
		env.Name, strings.Join(append(reachable, unreachable...), "; "))
}

// candidates lists the URLs to try, in order. A previously working URL is tried
// first, then the probe paths, so a spec that moves is found again.
func (f *Fetcher) candidates(env *config.Environment, cached Meta) ([]string, error) {
	if env.SpecURL != "" {
		u, err := resolveSpecURL(env, env.SpecURL)
		if err != nil {
			return nil, err
		}
		return []string{u}, nil
	}

	var urls []string
	seen := map[string]bool{}
	add := func(u string) {
		if u != "" && !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}

	add(cached.URL)
	for _, p := range ProbePaths {
		u, err := resolveSpecURL(env, p)
		if err != nil {
			return nil, err
		}
		add(u)
	}
	return urls, nil
}

func resolveSpecURL(env *config.Environment, raw string) (string, error) {
	ref, err := url.Parse(raw)
	if err != nil {
		return "", configError("env.%s.spec_url %q is not a URL: %w", env.Name, raw, err)
	}
	if ref.IsAbs() {
		return ref.String(), nil
	}
	resolved := *env.BaseURL
	resolved.Path = strings.TrimSuffix(env.BaseURL.Path, "/") + "/" + strings.TrimPrefix(ref.Path, "/")
	resolved.RawQuery = ref.RawQuery
	return resolved.String(), nil
}

func isUnauthorized(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}

// sameHost reports whether a spec URL lives on the API's own host. Credentials
// are only ever offered to that host: .blip.toml is committed and reviewed like
// any other file, and it must not be able to name somewhere else to send them.
func sameHost(rawURL string, base *url.URL) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, base.Host)
}

func (f *Fetcher) get(ctx context.Context, rawURL, etag string, authorize bool) (int, []byte, Meta, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, nil, Meta{}, err
	}
	req.Header.Set("Accept", "application/json, application/yaml;q=0.9, */*;q=0.5")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if authorize {
		if err := f.Authorize(ctx, req); err != nil {
			return 0, nil, Meta{}, err
		}
	}

	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, Meta{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, Meta{}, err
	}

	meta := Meta{
		URL:          rawURL,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		FetchedAt:    time.Now().UTC(),
	}
	return resp.StatusCode, body, meta, nil
}

// mergeMeta keeps the ETag blip already had when a 304 does not repeat it.
func mergeMeta(cached, fresh Meta) Meta {
	if fresh.ETag == "" {
		fresh.ETag = cached.ETag
	}
	if fresh.LastModified == "" {
		fresh.LastModified = cached.LastModified
	}
	return fresh
}

// looksLikeSpec keeps blip from caching an HTML error page that came back 200.
func looksLikeSpec(body []byte) bool {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		// YAML is not only served from a .yaml path, so try it whatever the URL.
		if err := yaml.Unmarshal(body, &doc); err != nil {
			return false
		}
	}
	_, openapi := doc["openapi"]
	_, swagger := doc["swagger"]
	return openapi || swagger
}

func specPath(dir string) string { return filepath.Join(dir, "spec") }
func metaPath(dir string) string { return filepath.Join(dir, "meta.json") }

func readCache(dir string) ([]byte, Meta) {
	body, err := os.ReadFile(specPath(dir))
	if err != nil {
		return nil, Meta{}
	}
	var meta Meta
	if raw, err := os.ReadFile(metaPath(dir)); err == nil {
		_ = json.Unmarshal(raw, &meta)
	}
	return body, meta
}

func writeCache(dir string, body []byte, meta Meta) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("caching spec: %w", err)
	}
	if err := writeFileAtomic(specPath(dir), body); err != nil {
		return fmt.Errorf("caching spec: %w", err)
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("caching spec metadata: %w", err)
	}
	if err := writeFileAtomic(metaPath(dir), raw); err != nil {
		return fmt.Errorf("caching spec metadata: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return nil
}
