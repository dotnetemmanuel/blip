package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotnetemmanuel/blip/internal/config"
	"github.com/dotnetemmanuel/blip/internal/detect"
	"github.com/dotnetemmanuel/blip/internal/output"
	"github.com/dotnetemmanuel/blip/internal/spec"
)

func TestUIRefusesWhenStdoutIsNotATerminal(t *testing.T) {
	f := newFixture(t, twoEnvs)

	got := f.run(t, "ui")

	if got.code != output.ExitUsage {
		t.Fatalf("exit = %d, want %d (%s)", got.code, output.ExitUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "terminal") {
		t.Errorf("stderr should say it needs a terminal:\n%s", got.stderr)
	}
	if got.stdout != "" {
		t.Errorf("stdout must stay empty, got %q", got.stdout)
	}
}

// ui discovers its own API, so a repo with no .blip.toml must fail on the missing
// terminal rather than on missing config.
func TestUINeedsNoConfig(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rt := &Runtime{
		Globals: &Globals{},
		Stdout:  stdout,
		Stderr:  stderr,
		Dir:     t.TempDir(),
	}

	if code := Run(rt, []string{"ui"}); code != output.ExitUsage {
		t.Fatalf("exit = %d, want %d (%s)", code, output.ExitUsage, stderr.String())
	}
}

// A pipe on stdin must not be handed to the renderer: it never types, and the
// screen would be drawn with no key able to close it.
func TestUITakesStdinOnlyFromATerminal(t *testing.T) {
	rt := &Runtime{Globals: &Globals{}, Stdin: os.Stdin}

	if got := rt.uiInput(); got != nil {
		t.Errorf("want no reader when stdin is not a terminal, got %T", got)
	}

	rt.StdinIsTTY = true
	if rt.uiInput() == nil {
		t.Error("want the terminal handed through when there is one")
	}
}

func TestUIDiscoversWhereConfigPoints(t *testing.T) {
	elsewhere := t.TempDir()
	rt := &Runtime{
		Globals: &Globals{ConfigPath: filepath.Join(elsewhere, ".blip.toml")},
		Dir:     t.TempDir(),
	}

	got, err := rt.discoveryRoot()
	if err != nil {
		t.Fatal(err)
	}
	if got != elsewhere {
		t.Errorf("discovery root = %q, want the directory --config names, %q", got, elsewhere)
	}
}

// The ladder looks for its config by name, so a config under another name would
// send ui to a different API from every other command in the same invocation.
func TestUIRefusesAConfigItCannotFind(t *testing.T) {
	rt := &Runtime{Globals: &Globals{ConfigPath: filepath.Join(t.TempDir(), "staging.toml")}}

	_, err := rt.discoveryRoot()
	if err == nil {
		t.Fatal("want a refusal rather than a silent divergence")
	}
	if code := output.ExitCodeFor(err); code != output.ExitUsage {
		t.Errorf("exit code = %d, want %d", code, output.ExitUsage)
	}
	if !strings.Contains(err.Error(), "staging.toml") {
		t.Errorf("want the offending name in the message, got %q", err)
	}
}

func TestTheDefaultThemeHasColors(t *testing.T) {
	th := defaultTheme()

	if th.Primary == "" || th.Text == "" || th.Muted == "" {
		t.Fatalf("the ui would render with no color at all: %+v", th)
	}
}

// testEnv builds a bare config.Environment for tests that exercise fetchAPI
// directly, without going through a .blip.toml.
func testEnv(t *testing.T, baseURL, specURL string) *config.Environment {
	t.Helper()
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	return &config.Environment{Name: "dev", BaseURL: u, SpecURL: specURL, Timeout: 5 * time.Second}
}

// blankRuntime is a Runtime with no .blip.toml in reach, isolated from the
// real spec cache. It is what a target from rung 2, 3 or 4 loads against:
// there is no config to merge routes from.
func blankRuntime(t *testing.T) *Runtime {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	return &Runtime{Globals: &Globals{}, Dir: t.TempDir(), Stderr: &bytes.Buffer{}}
}

func TestLoadAPIReadsSpecPathFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openapi.json")
	if err := os.WriteFile(path, []byte(`{"openapi":"3.0.1","info":{"title":"OnDisk","version":"1"},"paths":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	api, err := loadAPIFor(blankRuntime(t))(context.Background(), detect.Target{SpecPath: path})
	if err != nil {
		t.Fatalf("loadAPIFor: %v", err)
	}
	if api.Title != "OnDisk" {
		t.Errorf("api.Title = %q, want %q", api.Title, "OnDisk")
	}
}

func TestLoadAPIPrefersSpecPathOverTheFetcher(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openapi.json")
	if err := os.WriteFile(path, []byte(`{"openapi":"3.0.1","info":{"title":"FromDisk","version":"1"},"paths":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	t.Cleanup(srv.Close)

	target := detect.Target{SpecPath: path, Env: testEnv(t, srv.URL, srv.URL+"/openapi.json")}
	api, err := loadAPIFor(blankRuntime(t))(context.Background(), target)
	if err != nil {
		t.Fatalf("loadAPIFor: %v", err)
	}
	if api.Title != "FromDisk" {
		t.Errorf("api.Title = %q, want the document on disk, not the network", api.Title)
	}
	if called {
		t.Error("loadAPIFor fetched over the network even though SpecPath was set; rung 2 must never touch the network")
	}
}

func TestLoadAPIProbesWhenThereIsNoSpecPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi/v1.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openapi":"3.0.1","info":{"title":"FromNetwork","version":"1"},"paths":{}}`))
	}))
	t.Cleanup(srv.Close)

	target := detect.Target{Env: testEnv(t, srv.URL, "")}
	api, err := loadAPIFor(blankRuntime(t))(context.Background(), target)
	if err != nil {
		t.Fatalf("loadAPIFor: %v", err)
	}
	if api.Title != "FromNetwork" {
		t.Errorf("api.Title = %q, want %q", api.Title, "FromNetwork")
	}
}

func TestLoadAPISendsNoCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("request carried an Authorization header %q; the loader must never authenticate", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openapi":"3.0.1","info":{"title":"NoAuth","version":"1"},"paths":{}}`))
	}))
	t.Cleanup(srv.Close)

	target := detect.Target{Env: testEnv(t, srv.URL, srv.URL+"/openapi.json")}
	if _, err := loadAPIFor(blankRuntime(t))(context.Background(), target); err != nil {
		t.Fatalf("loadAPIFor: %v", err)
	}
}

// A 401 must not make the loader retry with credentials: there is nowhere for
// it to get any, and it must not go looking.
func TestLoadAPILeavesA401Unretried(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	target := detect.Target{Env: testEnv(t, srv.URL, "")}
	if _, err := loadAPIFor(blankRuntime(t))(context.Background(), target); err == nil {
		t.Fatal("want an error: every candidate returned 401")
	}
	if requests != len(spec.ProbePaths) {
		t.Errorf("requests = %d, want exactly one per probe path with no retry", requests)
	}
}

func TestLoadAPIFailsClearlyWithNoKnownSpecLocation(t *testing.T) {
	_, err := loadAPIFor(blankRuntime(t))(context.Background(), detect.Target{Title: "mystery"})
	if err == nil {
		t.Fatal("want an error when a target has neither SpecPath nor Env")
	}
	if !strings.Contains(err.Error(), "mystery") {
		t.Errorf("error = %q, want it to name the target", err)
	}
	if !strings.Contains(err.Error(), "spec_url") && !strings.Contains(err.Error(), "base_url") {
		t.Errorf("error = %q, want it to name a remedy", err)
	}
	if code := output.ExitCodeFor(err); code != output.ExitConfig {
		t.Errorf("exit code = %d, want %d", code, output.ExitConfig)
	}
}

func TestLoadAPIReportsAnUnreachableHost(t *testing.T) {
	target := detect.Target{Env: testEnv(t, "http://127.0.0.1:1", "")}
	_, err := loadAPIFor(blankRuntime(t))(context.Background(), target)
	if err == nil {
		t.Fatal("want an error when the spec host cannot be reached")
	}
	if code := output.ExitCodeFor(err); code != output.ExitTransport {
		t.Errorf("exit code = %d, want %d", code, output.ExitTransport)
	}
}

// A host that accepts the connection and never answers must not leave the
// "loading" screen sitting for as long as every candidate's own per-request
// timeout adds up to: the whole load needs its own bound. The environment's
// own Timeout is set far past that bound, so passing this test only because
// of request.NewClient's per-request timeout is ruled out.
func TestLoadAPIBoundsTheWholeLoadEvenWhenTheHostNeverAnswers(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-block
	}))
	t.Cleanup(func() {
		close(block)
		srv.Close()
	})

	target := detect.Target{Env: testEnv(t, srv.URL, "")}
	target.Env.Timeout = 30 * time.Second

	start := time.Now()
	_, err := loadAPIFor(blankRuntime(t))(context.Background(), target)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("want an error: the host never answered")
	}
	if elapsed > 7*time.Second {
		t.Errorf("load took %s, want it bounded near the loader's own timeout regardless of the environment's 30s one", elapsed)
	}
}

// writeConfig drops a minimal .blip.toml so rt.Config() resolves for real,
// the way a rung-1 target's loader call always finds one.
func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".blip.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func configuredTarget(t *testing.T, rt *Runtime) detect.Target {
	t.Helper()
	cfg, err := rt.Config()
	if err != nil {
		t.Fatal(err)
	}
	env, err := cfg.ResolveEnv("")
	if err != nil {
		t.Fatal(err)
	}
	return detect.Target{Title: cfg.Name, Env: env}
}

// The ordinary CLI works against blip-sandbox's own .blip.toml with the
// service down, because it uses the ETag cache. The ui loader must too.
func TestFetchAPIUsesTheCacheWhenTheServiceGoesDown(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	dir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi/v1.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openapi":"3.0.1","info":{"title":"Cached","version":"1"},"paths":{}}`))
	}))
	writeConfig(t, dir, fmt.Sprintf("name = \"cache-demo\"\ndefault_env = \"dev\"\n\n[env.dev]\nbase_url = %q\n", srv.URL))

	rt := &Runtime{Globals: &Globals{}, Dir: dir, Stderr: &bytes.Buffer{}}
	target := configuredTarget(t, rt)

	api, err := loadAPIFor(rt)(context.Background(), target)
	if err != nil {
		t.Fatalf("first load (service up): %v", err)
	}
	if api.Stale {
		t.Error("the first load should not be flagged stale; the service just answered")
	}

	srv.Close()

	// A fresh Runtime: a later `blip ui` is a separate process.
	stderr := &bytes.Buffer{}
	rt2 := &Runtime{Globals: &Globals{}, Dir: dir, Stderr: stderr}
	target2 := configuredTarget(t, rt2)

	api2, err := loadAPIFor(rt2)(context.Background(), target2)
	if err != nil {
		t.Fatalf("second load (service down) should succeed from the cache: %v", err)
	}
	if !api2.Stale {
		t.Error("want the second load flagged stale: it came from the cache because the service is down")
	}
	if stderr.Len() != 0 {
		t.Errorf("Warnf wrote to the real terminal while bubbletea owns the screen: %q", stderr.String())
	}
	found := false
	for _, w := range api2.LoadNotes {
		if strings.Contains(w, "cache") {
			found = true
		}
	}
	if !found {
		t.Errorf("want a note about the cache carried back on api.LoadNotes, got %v", api2.LoadNotes)
	}
}

// Runtime.ReportSpecWarnings only prints a spec's parse advisories when it
// was freshly fetched, or under --verbose, and stays quiet on a cached one.
// The ui loader must agree, or a cached load nags on every frame about
// something the plain CLI would say nothing about.
func TestFetchAPIGatesParseAdvisoriesToTheFreshFetchRule(t *testing.T) {
	noOpID := `{"openapi":"3.0.1","info":{"title":"Ungated","version":"1"},"paths":{` +
		`"/x":{"get":{"tags":["G"],"responses":{"200":{"description":"OK"}}}}}}`

	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi/v1.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(noOpID))
	}))
	writeConfig(t, dir, fmt.Sprintf("name = \"gate-demo\"\ndefault_env = \"dev\"\n\n[env.dev]\nbase_url = %q\n", srv.URL))

	rt := &Runtime{Globals: &Globals{}, Dir: dir, Stderr: &bytes.Buffer{}}
	target := configuredTarget(t, rt)

	api, err := loadAPIFor(rt)(context.Background(), target)
	if err != nil {
		t.Fatalf("first load (fresh fetch): %v", err)
	}
	if len(api.Warnings) == 0 {
		t.Fatal("want the derived-name advisory on a freshly fetched spec")
	}

	srv.Close()

	rt2 := &Runtime{Globals: &Globals{}, Dir: dir, Stderr: &bytes.Buffer{}}
	target2 := configuredTarget(t, rt2)
	api2, err := loadAPIFor(rt2)(context.Background(), target2)
	if err != nil {
		t.Fatalf("second load (from cache): %v", err)
	}
	if len(api2.Warnings) != 0 {
		t.Errorf("want no parse advisories on a cached load, got %v", api2.Warnings)
	}
	if len(api2.LoadNotes) == 0 {
		t.Error("want the cache note still present even though the advisory is gated: they are not the same thing")
	}

	rt3 := &Runtime{Globals: &Globals{Verbose: true}, Dir: dir, Stderr: &bytes.Buffer{}}
	target3 := configuredTarget(t, rt3)
	api3, err := loadAPIFor(rt3)(context.Background(), target3)
	if err != nil {
		t.Fatalf("third load (--verbose, from cache): %v", err)
	}
	if len(api3.Warnings) == 0 {
		t.Error("want --verbose to show the advisory even on a cached load")
	}
}

// A probe that finds nothing must not change what a later, unrelated plain
// command does: browsing is read-only with respect to the rest of blip.
func TestFetchAPIProbingLeavesNoMarkerForALaterPlainRun(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	writeConfig(t, dir, fmt.Sprintf("name = \"probe-demo\"\ndefault_env = \"dev\"\n\n[env.dev]\nbase_url = %q\n", srv.URL))

	rt := &Runtime{Globals: &Globals{}, Dir: dir, Stderr: &bytes.Buffer{}}
	target := configuredTarget(t, rt)

	if _, err := loadAPIFor(rt)(context.Background(), target); err == nil {
		t.Fatal("want an error: the server serves no spec anywhere")
	}
	probed := requests
	if probed == 0 {
		t.Fatal("test setup: the loader never actually probed the server")
	}

	cfg, err := rt.Config()
	if err != nil {
		t.Fatal(err)
	}
	env, err := cfg.ResolveEnv("")
	if err != nil {
		t.Fatal(err)
	}
	plain := &spec.Fetcher{Client: srv.Client()}
	if _, err := plain.Load(context.Background(), cfg.Name, env); err == nil {
		t.Fatal("Load succeeded with no spec anywhere")
	}
	if requests == probed {
		t.Error("a later plain run skipped probing, as if the ui session had left a no-spec marker behind")
	}
}

// A repo whose API is entirely hand-declared must not show an empty screen
// just because there is no spec behind it.
func TestFetchAPIFallsBackToDeclaredRoutesWhenTheSpecCannotBeFetched(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	writeConfig(t, dir, "name = \"routes-only\"\ndefault_env = \"dev\"\n\n"+
		"[env.dev]\nbase_url = \"http://127.0.0.1:1\"\n\n"+
		"[[route]]\nname = \"reindex\"\nmethod = \"POST\"\npath = \"/admin/reindex/{tenant}\"\nsummary = \"Rebuild the index\"\n")

	rt := &Runtime{Globals: &Globals{}, Dir: dir, Stderr: &bytes.Buffer{}}
	target := configuredTarget(t, rt)

	api, err := loadAPIFor(rt)(context.Background(), target)
	if err != nil {
		t.Fatalf("want the hand-declared route to fill the screen even though the spec is unreachable: %v", err)
	}
	if api.Find("reindex") == nil {
		t.Errorf("want the reindex route in the API, got operations %+v", api.Operations)
	}
}

// Routes must be merged alongside a spec that did load, not only as a
// fallback when it fails.
func TestFetchAPIMergesDeclaredRoutesAlongsideASuccessfulSpec(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi/v1.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openapi":"3.0.1","info":{"title":"WithRoutes","version":"1"},"paths":{}}`))
	}))
	t.Cleanup(srv.Close)
	writeConfig(t, dir, fmt.Sprintf("name = \"with-routes\"\ndefault_env = \"dev\"\n\n[env.dev]\nbase_url = %q\n\n"+
		"[[route]]\nname = \"reindex\"\nmethod = \"POST\"\npath = \"/admin/reindex/{tenant}\"\nsummary = \"Rebuild the index\"\n", srv.URL))

	rt := &Runtime{Globals: &Globals{}, Dir: dir, Stderr: &bytes.Buffer{}}
	target := configuredTarget(t, rt)

	api, err := loadAPIFor(rt)(context.Background(), target)
	if err != nil {
		t.Fatalf("loadAPIFor: %v", err)
	}
	if api.Find("reindex") == nil {
		t.Errorf("want the reindex route merged alongside the fetched spec, got operations %+v", api.Operations)
	}
}

// The ui package's own tests inject their own Discover, LoadAPI and Send, so a
// capability the command layer forgets to bind is invisible to all of them: the
// explorer would browse fine and simply never send anything.
func TestUIOptionsBindEveryCapability(t *testing.T) {
	rt := &Runtime{Globals: &Globals{}, Dir: t.TempDir()}
	opts := uiOptions(rt, rt.Dir)

	if opts.Discover == nil {
		t.Error("Discover is not bound, so the explorer would find nothing")
	}
	if opts.LoadAPI == nil {
		t.Error("LoadAPI is not bound, so no spec would ever load")
	}
	if opts.Send == nil {
		t.Error("Send is not bound, so every send would refuse with no base URL")
	}
}

// sendFor is the explorer's only path to the network, and nothing exercised it:
// every test in the ui package injects its own Send, so the host pin, the
// profile pick and the readonly gate were reasoned about rather than run.
// XDG_CONFIG_HOME is redirected so this reads a credentials file the test wrote
// rather than the one belonging to whoever is running it.
func TestSendForEnforcesTheGateAndTheHostPin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	local, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := url.Parse("https://api.example.test")
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	credsDir := filepath.Join(home, "blip")
	if err := os.MkdirAll(credsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	credentials := `[unpinned]
type  = "bearer"
token = "unpinned-secret"

[elsewhere]
type  = "bearer"
token = "elsewhere-secret"
hosts = ["other.test"]
`
	if err := os.WriteFile(filepath.Join(credsDir, "credentials.toml"), []byte(credentials), 0o600); err != nil {
		t.Fatal(err)
	}

	// A profile only ever reaches the explorer through .blip.toml, so a runtime
	// with a profile and no config is not a state that can occur. The config is
	// written for that reason, not because sendFor reads it.
	newRuntime := func() *Runtime {
		dir := t.TempDir()
		blipToml := "name = \"t\"\ndefault_env = \"dev\"\n\n[env.dev]\nbase_url = \"" + srv.URL + "\"\n"
		if err := os.WriteFile(filepath.Join(dir, ".blip.toml"), []byte(blipToml), 0o644); err != nil {
			t.Fatal(err)
		}
		return &Runtime{Globals: &Globals{}, Dir: dir, Stderr: &bytes.Buffer{}}
	}
	newReq := func(method string, base *url.URL) *http.Request {
		req, err := http.NewRequest(method, base.String()+"/api/things", nil)
		if err != nil {
			t.Fatal(err)
		}
		return req
	}

	t.Run("readonly refuses a mutation at the door", func(t *testing.T) {
		send := sendFor(newRuntime())
		env := &config.Environment{Name: "locked", BaseURL: local, Readonly: true}

		_, err := send(context.Background(), env, newReq(http.MethodDelete, local))

		if err == nil {
			t.Fatal("a readonly environment sent a DELETE")
		}
		if got := output.ExitCodeFor(err); got != output.ExitBlocked {
			t.Errorf("exit = %d, want %d", got, output.ExitBlocked)
		}
	})

	t.Run("readonly still lets a read through", func(t *testing.T) {
		send := sendFor(newRuntime())
		env := &config.Environment{Name: "locked", BaseURL: local, Readonly: true}

		if _, err := send(context.Background(), env, newReq(http.MethodGet, local)); err != nil {
			t.Errorf("a read was refused in a readonly environment: %v", err)
		}
	})

	// .blip.toml is committed and may come from a repo you merely cloned, so it
	// must not be able to choose both the secret and where the secret goes. An
	// unpinned profile reaches loopback and nowhere else.
	t.Run("an unpinned profile cannot reach a remote host", func(t *testing.T) {
		send := sendFor(newRuntime())
		env := &config.Environment{Name: "dev", BaseURL: remote, Auth: "unpinned"}

		_, err := send(context.Background(), env, newReq(http.MethodGet, remote))

		if err == nil {
			t.Fatal("an unpinned profile reached a remote host")
		}
		if got := err.Error(); !strings.Contains(got, "hosts") {
			t.Errorf("error = %q, want the host pin to be what refused it", got)
		}
		if got := output.ExitCodeFor(err); got != output.ExitBlocked {
			t.Errorf("exit = %d, want %d", got, output.ExitBlocked)
		}
	})

	t.Run("an unpinned profile still reaches loopback", func(t *testing.T) {
		send := sendFor(newRuntime())
		env := &config.Environment{Name: "dev", BaseURL: local, Auth: "unpinned"}

		if _, err := send(context.Background(), env, newReq(http.MethodGet, local)); err != nil {
			t.Errorf("an unpinned profile was refused loopback: %v", err)
		}
	})

	t.Run("a pinned profile cannot reach a host outside its list", func(t *testing.T) {
		send := sendFor(newRuntime())
		env := &config.Environment{Name: "dev", BaseURL: remote, Auth: "elsewhere"}

		_, err := send(context.Background(), env, newReq(http.MethodGet, remote))

		if err == nil {
			t.Fatal("a pinned profile reached a host its list does not name")
		}
		if got := err.Error(); !strings.Contains(got, "api.example.test") {
			t.Errorf("error = %q, want it to name the host it refused", got)
		}
		// Without this the test passes on a DNS failure, which means the
		// credential was applied and the request went out before the network
		// stopped it. Blocked is the only answer that proves the pin refused it.
		if got := output.ExitCodeFor(err); got != output.ExitBlocked {
			t.Errorf("exit = %d, want %d: the pin must refuse it before it is sent", got, output.ExitBlocked)
		}
	})

	// --profile is the second way a profile arrives, and it does not need a
	// config to have come from. A bearer credential needs no environment either,
	// so a repo where discovery found the API by probing can still send.
	t.Run("a bearer profile sends in a repo with no config", func(t *testing.T) {
		rt := &Runtime{Globals: &Globals{Profile: "unpinned"}, Dir: t.TempDir(), Stderr: &bytes.Buffer{}}
		send := sendFor(rt)
		env := &config.Environment{Name: "dev", BaseURL: local}

		if _, err := send(context.Background(), env, newReq(http.MethodGet, local)); err != nil {
			t.Errorf("--profile with no .blip.toml could not send: %v", err)
		}
	})

	t.Run("no profile means no credential and no pin to check", func(t *testing.T) {
		send := sendFor(newRuntime())
		env := &config.Environment{Name: "dev", BaseURL: local}

		if _, err := send(context.Background(), env, newReq(http.MethodGet, local)); err != nil {
			t.Errorf("a target discovered without a config could not send: %v", err)
		}
	})
}
