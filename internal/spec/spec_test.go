package spec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotnetemmanuel/blip/internal/config"
	"github.com/dotnetemmanuel/blip/internal/output"
)

const minimalSpec = `{"openapi":"3.0.1","info":{"title":"orders","version":"1"},"paths":{}}`

func env(t *testing.T, baseURL, specURL string) *config.Environment {
	t.Helper()
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	return &config.Environment{Name: "dev", BaseURL: u, SpecURL: specURL}
}

// specServer serves a spec with ETag support and counts what it was asked for.
type specServer struct {
	*httptest.Server
	requests   []string
	etag       string
	body       string
	status     int
	conditions int
}

func newSpecServer(t *testing.T, path string) *specServer {
	t.Helper()
	s := &specServer{etag: `"v1"`, body: minimalSpec, status: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.requests = append(s.requests, r.URL.Path)
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if match := r.Header.Get("If-None-Match"); match != "" {
			s.conditions++
			if match == s.etag {
				w.Header().Set("ETag", s.etag)
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		w.Header().Set("ETag", s.etag)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(s.body))
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func newFetcher(t *testing.T, srv *specServer) (*Fetcher, *[]string) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var warnings []string
	f := &Fetcher{
		Client: srv.Client(),
		Warnf:  func(format string, args ...any) { warnings = append(warnings, format) },
	}
	return f, &warnings
}

func TestLoadFetchesThenRevalidates(t *testing.T) {
	srv := newSpecServer(t, "/openapi/v1.json")
	f, _ := newFetcher(t, srv)
	e := env(t, srv.URL, "")

	first, err := f.Load(context.Background(), "orders", e)
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if first.Status != StatusFetched {
		t.Errorf("Status = %q, want %q", first.Status, StatusFetched)
	}
	if string(first.Data) != minimalSpec {
		t.Errorf("Data = %q, want the spec", first.Data)
	}

	second, err := f.Load(context.Background(), "orders", e)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if second.Status != StatusRevalidated {
		t.Errorf("Status = %q, want %q", second.Status, StatusRevalidated)
	}
	if srv.conditions != 1 {
		t.Errorf("conditional requests = %d, want the second run to send If-None-Match", srv.conditions)
	}
	if string(second.Data) != minimalSpec {
		t.Errorf("Data = %q, want the cached spec", second.Data)
	}
}

func TestLoadTakesANewSpecWhenTheETagChanges(t *testing.T) {
	srv := newSpecServer(t, "/openapi/v1.json")
	f, _ := newFetcher(t, srv)
	e := env(t, srv.URL, "")

	if _, err := f.Load(context.Background(), "orders", e); err != nil {
		t.Fatal(err)
	}

	updated := `{"openapi":"3.0.1","info":{"title":"orders","version":"2"},"paths":{}}`
	srv.etag = `"v2"`
	srv.body = updated

	got, err := f.Load(context.Background(), "orders", e)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != updated {
		t.Errorf("Data = %q, want the replaced spec", got.Data)
	}
	if got.Status != StatusFetched {
		t.Errorf("Status = %q, want %q", got.Status, StatusFetched)
	}
}

func TestProbeOrder(t *testing.T) {
	tests := []struct {
		name  string
		serve string
	}{
		{"dotnet 9 openapi", "/openapi/v1.json"},
		{"swashbuckle", "/swagger/v1/swagger.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newSpecServer(t, tt.serve)
			f, _ := newFetcher(t, srv)

			got, err := f.Load(context.Background(), "orders", env(t, srv.URL, ""))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !strings.HasSuffix(got.Meta.URL, tt.serve) {
				t.Errorf("URL = %q, want it to end in %q", got.Meta.URL, tt.serve)
			}
		})
	}
}

func TestProbeStopsAtTheFirstHit(t *testing.T) {
	srv := newSpecServer(t, "/openapi/v1.json")
	f, _ := newFetcher(t, srv)

	if _, err := f.Load(context.Background(), "orders", env(t, srv.URL, "")); err != nil {
		t.Fatal(err)
	}
	if len(srv.requests) != 1 {
		t.Errorf("requests = %v, want the probe to stop at the first hit", srv.requests)
	}
}

func TestYAMLProbe(t *testing.T) {
	srv := newSpecServer(t, "/openapi/v1.yaml")
	srv.body = "openapi: 3.0.1\ninfo:\n  title: orders\n  version: \"1\"\npaths: {}\n"
	f, _ := newFetcher(t, srv)

	got, err := f.Load(context.Background(), "orders", env(t, srv.URL, ""))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.HasSuffix(got.Meta.URL, "/openapi/v1.yaml") {
		t.Errorf("URL = %q, want the YAML path", got.Meta.URL)
	}
}

func TestExplicitSpecURL(t *testing.T) {
	srv := newSpecServer(t, "/custom/spec.json")
	f, _ := newFetcher(t, srv)

	got, err := f.Load(context.Background(), "orders", env(t, srv.URL, "/custom/spec.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.HasSuffix(got.Meta.URL, "/custom/spec.json") {
		t.Errorf("URL = %q, want the configured path", got.Meta.URL)
	}
	if len(srv.requests) != 1 {
		t.Errorf("requests = %v, want no probing when spec_url is set", srv.requests)
	}
}

func TestAbsoluteSpecURLIsUsedAsGiven(t *testing.T) {
	srv := newSpecServer(t, "/elsewhere.json")
	f, _ := newFetcher(t, srv)

	got, err := f.Load(context.Background(), "orders", env(t, "http://unused.invalid", srv.URL+"/elsewhere.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Meta.URL != srv.URL+"/elsewhere.json" {
		t.Errorf("URL = %q, want the absolute spec_url", got.Meta.URL)
	}
}

func TestSpecURLResolvesAgainstABasePath(t *testing.T) {
	srv := newSpecServer(t, "/api/openapi/v1.json")
	f, _ := newFetcher(t, srv)

	got, err := f.Load(context.Background(), "orders", env(t, srv.URL+"/api", "/openapi/v1.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.HasSuffix(got.Meta.URL, "/api/openapi/v1.json") {
		t.Errorf("URL = %q, want the base path kept", got.Meta.URL)
	}
}

func TestA200ThatIsNotASpecIsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>Swagger UI</body></html>"))
	}))
	defer srv.Close()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	f := &Fetcher{Client: srv.Client()}

	_, err := f.Load(context.Background(), "orders", env(t, srv.URL, ""))

	if err == nil {
		t.Fatal("Load succeeded on an HTML page, want an error")
	}
	if !strings.Contains(err.Error(), "not an OpenAPI document") {
		t.Errorf("err = %q, want it to say what was wrong", err)
	}
}

func TestOfflineWithAWarmCache(t *testing.T) {
	srv := newSpecServer(t, "/openapi/v1.json")
	f, _ := newFetcher(t, srv)
	e := env(t, srv.URL, "")

	if _, err := f.Load(context.Background(), "orders", e); err != nil {
		t.Fatal(err)
	}
	before := len(srv.requests)

	f.Offline = true
	got, err := f.Load(context.Background(), "orders", e)
	if err != nil {
		t.Fatalf("offline Load: %v", err)
	}
	if got.Status != StatusCached {
		t.Errorf("Status = %q, want %q", got.Status, StatusCached)
	}
	if len(srv.requests) != before {
		t.Errorf("requests = %d, want --offline to touch no network", len(srv.requests)-before)
	}
}

func TestOfflineWithAColdCacheIsExitThree(t *testing.T) {
	srv := newSpecServer(t, "/openapi/v1.json")
	f, _ := newFetcher(t, srv)
	f.Offline = true

	_, err := f.Load(context.Background(), "orders", env(t, srv.URL, ""))

	if err == nil {
		t.Fatal("Load succeeded with a cold cache, want an error")
	}
	if got := output.ExitCodeFor(err); got != output.ExitConfig {
		t.Errorf("exit code = %d, want %d", got, output.ExitConfig)
	}
	if !strings.Contains(err.Error(), "--offline") {
		t.Errorf("err = %q, want it to explain the cause", err)
	}
}

func TestBackendDownWithAWarmCacheStillWorks(t *testing.T) {
	srv := newSpecServer(t, "/openapi/v1.json")
	f, warnings := newFetcher(t, srv)
	e := env(t, srv.URL, "")

	if _, err := f.Load(context.Background(), "orders", e); err != nil {
		t.Fatal(err)
	}
	srv.Close()

	got, err := f.Load(context.Background(), "orders", e)
	if err != nil {
		t.Fatalf("Load with a dead backend: %v", err)
	}
	if got.Status != StatusStale {
		t.Errorf("Status = %q, want %q", got.Status, StatusStale)
	}
	if string(got.Data) != minimalSpec {
		t.Errorf("Data = %q, want the cached spec", got.Data)
	}
	if len(*warnings) == 0 {
		t.Error("no warning was emitted for a stale spec")
	}
}

func TestBackendDownWithAColdCacheIsATransportError(t *testing.T) {
	srv := newSpecServer(t, "/openapi/v1.json")
	f, _ := newFetcher(t, srv)
	e := env(t, srv.URL, "")
	srv.Close()

	_, err := f.Load(context.Background(), "orders", e)

	if err == nil {
		t.Fatal("Load succeeded against a dead backend with no cache")
	}
	if got := output.ExitCodeFor(err); got != output.ExitTransport {
		t.Errorf("exit code = %d, want %d", got, output.ExitTransport)
	}
}

func TestRefreshSkipsTheConditionalRequest(t *testing.T) {
	srv := newSpecServer(t, "/openapi/v1.json")
	f, _ := newFetcher(t, srv)
	e := env(t, srv.URL, "")

	if _, err := f.Load(context.Background(), "orders", e); err != nil {
		t.Fatal(err)
	}

	f.Refresh = true
	got, err := f.Load(context.Background(), "orders", e)
	if err != nil {
		t.Fatal(err)
	}
	if srv.conditions != 0 {
		t.Errorf("conditional requests = %d, want --refresh to send none", srv.conditions)
	}
	if before := len(srv.requests); before < 2 {
		t.Errorf("requests = %d, want --refresh to go to the server", before)
	}
	// The document came back byte-identical, which is what the status reports.
	if got.Status != StatusRevalidated {
		t.Errorf("Status = %q, want %q", got.Status, StatusRevalidated)
	}
}

func TestSpecThatMovesIsFoundAgain(t *testing.T) {
	srv := newSpecServer(t, "/openapi/v1.json")
	f, warnings := newFetcher(t, srv)
	e := env(t, srv.URL, "")

	if _, err := f.Load(context.Background(), "orders", e); err != nil {
		t.Fatal(err)
	}

	moved := newSpecServerAt(t, srv, "/swagger/v1/swagger.json")
	_ = moved

	got, err := f.Load(context.Background(), "orders", e)
	if err != nil {
		t.Fatalf("Load after the spec moved: %v", err)
	}
	if !strings.HasSuffix(got.Meta.URL, "/swagger/v1/swagger.json") {
		t.Errorf("URL = %q, want the new location", got.Meta.URL)
	}
	if len(*warnings) == 0 {
		t.Error("no warning was emitted when the spec moved")
	}
}

// newSpecServerAt repoints an existing server at a different spec path.
func newSpecServerAt(t *testing.T, s *specServer, path string) *specServer {
	t.Helper()
	s.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests = append(s.requests, r.URL.Path)
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("ETag", s.etag)
		_, _ = w.Write([]byte(s.body))
	})
	return s
}

func TestCacheIsPerEnvironment(t *testing.T) {
	srv := newSpecServer(t, "/openapi/v1.json")
	f, _ := newFetcher(t, srv)

	dev := env(t, srv.URL, "")
	prod := env(t, srv.URL, "")
	prod.Name = "prod"

	if _, err := f.Load(context.Background(), "orders", dev); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Load(context.Background(), "orders", prod); err != nil {
		t.Fatal(err)
	}

	base := filepath.Join(os.Getenv("XDG_CACHE_HOME"), "blip", "specs", "orders")
	for _, name := range []string{"dev", "prod"} {
		if _, err := os.Stat(filepath.Join(base, name, "spec")); err != nil {
			t.Errorf("no cached spec for %s: %v", name, err)
		}
	}
}

func TestLooksLikeSpec(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"openapi 3", `{"openapi":"3.0.1"}`, true},
		{"swagger 2", `{"swagger":"2.0"}`, true},
		{"json but not a spec", `{"message":"not found"}`, false},
		{"html", `<html></html>`, false},
		{"empty", ``, false},
		{"yaml spec", "openapi: 3.0.1\n", true},
		{"yaml spec served from a path with no extension", "swagger: \"2.0\"\n", true},
		{"yaml that is not a spec", "message: not found\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LooksLikeSpec([]byte(tt.body)); got != tt.want {
				t.Errorf("LooksLikeSpec = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNoSpecIsRememberedForAWhile(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f := &Fetcher{Client: srv.Client()}
	e := env(t, srv.URL, "")

	if _, err := f.Load(context.Background(), "legacy", e); err == nil {
		t.Fatal("Load succeeded with no spec anywhere")
	}
	probed := requests
	if probed != len(ProbePaths) {
		t.Fatalf("requests = %d, want one per probe path", probed)
	}

	if _, err := f.Load(context.Background(), "legacy", e); err == nil {
		t.Fatal("Load succeeded with no spec anywhere")
	}
	if requests != probed {
		t.Errorf("requests = %d, want the second run to skip probing", requests-probed)
	}

	f.Refresh = true
	if _, err := f.Load(context.Background(), "legacy", e); err == nil {
		t.Fatal("Load succeeded with no spec anywhere")
	}
	if requests == probed {
		t.Error("--refresh did not re-probe")
	}
}

func TestNoSpecIsNotRememberedWhenTheHostIsUnreachable(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := srv.Client()
	srv.Close()

	f := &Fetcher{Client: client}
	e := env(t, srv.URL, "")

	if _, err := f.Load(context.Background(), "legacy", e); err == nil {
		t.Fatal("Load succeeded against a dead host")
	}

	dir, err := CacheDir("legacy", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(noSpecMarker(dir)); err == nil {
		t.Error("a dead host was remembered as having no spec; it may simply have been down")
	}
}

// A caller that only browses (blip ui) must not leave behind the marker that
// makes a later, unrelated run refuse to re-probe.
func TestSkipNegativeCacheLeavesNoMarkerBehind(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f := &Fetcher{Client: srv.Client(), SkipNegativeCache: true}
	e := env(t, srv.URL, "")

	if _, err := f.Load(context.Background(), "legacy", e); err == nil {
		t.Fatal("Load succeeded with no spec anywhere")
	}

	dir, err := CacheDir("legacy", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(noSpecMarker(dir)); err == nil {
		t.Error("SkipNegativeCache did not stop the no-spec marker from being written")
	}

	// A later plain run, with the field left at its zero value, must probe
	// again rather than trusting a marker that was never written.
	requests := 0
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv2.Close()
	plain := &Fetcher{Client: srv2.Client()}
	e2 := env(t, srv2.URL, "")
	if _, err := plain.Load(context.Background(), "legacy", e2); err == nil {
		t.Fatal("Load succeeded with no spec anywhere")
	}
	if requests == 0 {
		t.Error("a later plain run skipped probing, as if the browsing run had left a marker behind")
	}
}

func TestUnchangedBytesCountAsRevalidatedWithoutAnETag(t *testing.T) {
	// Microsoft.AspNetCore.OpenApi serves no ETag, so every run is a full fetch
	// and only the bytes can say whether the spec actually moved.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	body := minimalSpec
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi/v1.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	f := &Fetcher{Client: srv.Client()}
	e := env(t, srv.URL, "")

	first, err := f.Load(context.Background(), "orders", e)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != StatusFetched {
		t.Errorf("Status = %q, want %q on the first run", first.Status, StatusFetched)
	}

	second, err := f.Load(context.Background(), "orders", e)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != StatusRevalidated {
		t.Errorf("Status = %q, want identical bytes to count as unchanged", second.Status)
	}

	body = `{"openapi":"3.0.1","info":{"title":"orders","version":"2"},"paths":{}}`
	third, err := f.Load(context.Background(), "orders", e)
	if err != nil {
		t.Fatal(err)
	}
	if third.Status != StatusFetched {
		t.Errorf("Status = %q, want a changed spec to read as fetched", third.Status)
	}
}

func TestCredentialsNeverGoToAForeignSpecHost(t *testing.T) {
	// .blip.toml is committed and reviewed like any other file. It must not be
	// able to name a host to send the user's credentials to.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var sawAuth []string
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = append(sawAuth, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer foreign.Close()

	authorized := false
	f := &Fetcher{
		Client: foreign.Client(),
		Authorize: func(_ context.Context, req *http.Request) error {
			authorized = true
			req.Header.Set("Authorization", "Bearer super-secret")
			return nil
		},
	}

	_, err := f.Load(context.Background(), "orders", env(t, "https://api.internal", foreign.URL+"/spec.json"))

	if err == nil {
		t.Fatal("Load succeeded, want a refusal")
	}
	if got := output.ExitCodeFor(err); got != output.ExitBlocked {
		t.Errorf("exit code = %d, want %d", got, output.ExitBlocked)
	}
	if authorized {
		t.Error("credentials were resolved for a host that is not the API")
	}
	for _, header := range sawAuth {
		if header != "" {
			t.Errorf("the foreign host received %q", header)
		}
	}
}

func TestCredentialsStillGoToTheAPIsOwnSpecEndpoint(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(minimalSpec))
	}))
	defer srv.Close()

	f := &Fetcher{
		Client: srv.Client(),
		Authorize: func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer ok")
			return nil
		},
	}

	got, err := f.Load(context.Background(), "orders", env(t, srv.URL, "/spec.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(got.Data) != minimalSpec {
		t.Errorf("Data = %q, want the spec", got.Data)
	}
}

func TestASpecEndpointThatRefusesTheCredentialsIsAnAuthError(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	f := &Fetcher{
		Client:    srv.Client(),
		Authorize: func(_ context.Context, req *http.Request) error { return nil },
	}

	_, err := f.Load(context.Background(), "orders", env(t, srv.URL, "/spec.json"))
	if err == nil {
		t.Fatal("Load succeeded against a 403")
	}
	if got := output.ExitCodeFor(err); got != output.ExitAuth {
		t.Errorf("exit code = %d, want %d", got, output.ExitAuth)
	}
}

func TestATransientFailureIsNotRememberedAsNoSpec(t *testing.T) {
	// An agent restarting a backend hits this: a 503 must not lock blip out for
	// the whole negative-cache window.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	ready := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path != "/openapi/v1.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(minimalSpec))
	}))
	defer srv.Close()

	f := &Fetcher{Client: srv.Client()}
	e := env(t, srv.URL, "")

	if _, err := f.Load(context.Background(), "orders", e); err == nil {
		t.Fatal("Load succeeded while the service was starting")
	}

	ready = true
	got, err := f.Load(context.Background(), "orders", e)
	if err != nil {
		t.Fatalf("Load once the service was up: %v", err)
	}
	if string(got.Data) != minimalSpec {
		t.Errorf("Data = %q, want the spec", got.Data)
	}
}

func TestA304RefreshesWhatIsRemembered(t *testing.T) {
	srv := newSpecServer(t, "/openapi/v1.json")
	f, _ := newFetcher(t, srv)
	e := env(t, srv.URL, "")

	if _, err := f.Load(context.Background(), "orders", e); err != nil {
		t.Fatal(err)
	}
	got, err := f.Load(context.Background(), "orders", e)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusRevalidated {
		t.Fatalf("Status = %q, want %q", got.Status, StatusRevalidated)
	}

	dir, err := CacheDir("orders", "dev")
	if err != nil {
		t.Fatal(err)
	}
	_, meta := readCache(dir)
	if meta.ETag != `"v1"` {
		t.Errorf("persisted etag = %q, want the revalidated one", meta.ETag)
	}
	if meta.FetchedAt.IsZero() {
		t.Error("the revalidation was not written back to disk")
	}
}

func TestASpecURLMustMatchTheOriginNotJustTheHost(t *testing.T) {
	// Same host, plaintext scheme: the credential would leave TLS.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	base := env(t, srv.URL, srv.URL+"/spec")
	base.BaseURL.Scheme = "https"

	f := &Fetcher{
		Client: srv.Client(),
		Authorize: func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer super-secret")
			return nil
		},
	}

	_, err := f.Load(context.Background(), "orders", base)

	if err == nil {
		t.Fatal("Load succeeded, want a refusal")
	}
	if got := output.ExitCodeFor(err); got != output.ExitBlocked {
		t.Errorf("exit code = %d, want %d", got, output.ExitBlocked)
	}
	for _, header := range seen {
		if header != "" {
			t.Errorf("a credential went to the plaintext origin: %q", header)
		}
	}
}

func TestCacheDirKeepsAHostileNameInsideTheCache(t *testing.T) {
	// name and the environment both come from a committed .blip.toml.
	t.Setenv("XDG_CACHE_HOME", "/tmp/blip-cache-test")

	dir, err := CacheDir("../../../../etc/evil", "../../prod")
	if err != nil {
		t.Fatal(err)
	}
	root := "/tmp/blip-cache-test/blip/specs/"
	if !strings.HasPrefix(dir, root) {
		t.Errorf("CacheDir = %q, want it under the cache root", dir)
	}
	// The name may keep its dots; what matters is that no component is a climb
	// and no separator was smuggled in, so the path cannot leave the root.
	rest := strings.TrimPrefix(dir, root)
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		t.Errorf("CacheDir = %q, want exactly name/env under the root, got %d parts", dir, len(parts))
	}
	for _, part := range parts {
		if part == ".." || part == "." || part == "" {
			t.Errorf("CacheDir = %q has a traversal component %q", dir, part)
		}
	}
	if filepath.Clean(dir) != dir {
		t.Errorf("CacheDir = %q does not survive Clean, so it can still move", dir)
	}
}
