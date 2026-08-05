package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotnetemmanuel/blip/internal/detect"
	"github.com/dotnetemmanuel/blip/internal/output"
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

func TestLoadAPIReadsSpecPathFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openapi.json")
	if err := os.WriteFile(path, []byte(`{"openapi":"3.0.1","info":{"title":"OnDisk","version":"1"},"paths":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	api, err := loadAPI(context.Background(), detect.Target{SpecPath: path})
	if err != nil {
		t.Fatalf("loadAPI: %v", err)
	}
	if api.Title != "OnDisk" {
		t.Errorf("api.Title = %q, want %q", api.Title, "OnDisk")
	}
}

func TestLoadAPIPrefersSpecPathOverSpecURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openapi.json")
	if err := os.WriteFile(path, []byte(`{"openapi":"3.0.1","info":{"title":"FromDisk","version":"1"},"paths":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	t.Cleanup(srv.Close)

	api, err := loadAPI(context.Background(), detect.Target{SpecPath: path, SpecURL: srv.URL + "/openapi.json"})
	if err != nil {
		t.Fatalf("loadAPI: %v", err)
	}
	if api.Title != "FromDisk" {
		t.Errorf("api.Title = %q, want the document on disk, not the network", api.Title)
	}
	if called {
		t.Error("loadAPI fetched SpecURL even though SpecPath was set; rung 2 must never touch the network")
	}
}

func TestLoadAPIFetchesSpecURLWhenThereIsNoSpecPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openapi":"3.0.1","info":{"title":"FromNetwork","version":"1"},"paths":{}}`))
	}))
	t.Cleanup(srv.Close)

	api, err := loadAPI(context.Background(), detect.Target{SpecURL: srv.URL + "/openapi.json"})
	if err != nil {
		t.Fatalf("loadAPI: %v", err)
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

	if _, err := loadAPI(context.Background(), detect.Target{SpecURL: srv.URL + "/openapi.json"}); err != nil {
		t.Fatalf("loadAPI: %v", err)
	}
}

func TestLoadAPIFailsClearlyWithNoKnownSpecLocation(t *testing.T) {
	_, err := loadAPI(context.Background(), detect.Target{Title: "mystery"})
	if err == nil {
		t.Fatal("want an error when a target has neither SpecPath nor SpecURL")
	}
	if !strings.Contains(err.Error(), "mystery") {
		t.Errorf("error = %q, want it to name the target", err)
	}
	if code := output.ExitCodeFor(err); code != output.ExitConfig {
		t.Errorf("exit code = %d, want %d", code, output.ExitConfig)
	}
}

func TestLoadAPIReportsAnUnreachableHost(t *testing.T) {
	_, err := loadAPI(context.Background(), detect.Target{SpecURL: "http://127.0.0.1:1/openapi.json"})
	if err == nil {
		t.Fatal("want an error when the spec host cannot be reached")
	}
	if code := output.ExitCodeFor(err); code != output.ExitTransport {
		t.Errorf("exit code = %d, want %d", code, output.ExitTransport)
	}
}
