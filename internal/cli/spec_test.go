package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dotnetemmanuel/blip/internal/output"
)

const fixtureSpec = `{"openapi":"3.0.1","info":{"title":"orders","version":"1"},"paths":{}}`

// specStub serves a spec at a probe path and counts conditional requests.
type specStub struct {
	*httptest.Server
	conditions int
	requests   int
	protected  bool
}

func newSpecStub(t *testing.T) *specStub {
	t.Helper()
	s := &specStub{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests++
		if r.URL.Path != "/openapi/v1.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if s.protected && r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			s.conditions++
			w.Header().Set("ETag", `"v1"`)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixtureSpec))
	}))
	t.Cleanup(s.Close)
	return s
}

func specFixture(t *testing.T, srv *specStub) *fixture {
	t.Helper()
	f := newFixture(t, `
name = "orders"
default_env = "dev"

[env.dev]
base_url = "`+srv.URL+`"
auth = "orders-dev"
`)
	f.writeCredentials(t, bearerCreds)
	return f
}

func TestSpecFetchesThenRevalidates(t *testing.T) {
	srv := newSpecStub(t)
	f := specFixture(t, srv)

	first := f.run(t, "spec")
	if first.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", first.code, first.stderr)
	}
	if strings.TrimSpace(first.stdout) != fixtureSpec {
		t.Errorf("stdout = %q, want the spec", first.stdout)
	}

	second := f.run(t, "spec")
	if second.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", second.code, second.stderr)
	}
	if srv.conditions != 1 {
		t.Errorf("conditional requests = %d, want the second run to revalidate", srv.conditions)
	}
	if strings.TrimSpace(second.stdout) != fixtureSpec {
		t.Errorf("stdout = %q, want the cached spec", second.stdout)
	}
}

func TestSpecMetaAndPath(t *testing.T) {
	srv := newSpecStub(t)
	f := specFixture(t, srv)

	path := f.run(t, "spec", "--path")
	if path.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", path.code, path.stderr)
	}
	if !strings.Contains(path.stdout, "specs/orders/dev/spec") {
		t.Errorf("stdout = %q, want the cache path", path.stdout)
	}
	if _, err := os.Stat(strings.TrimSpace(path.stdout)); err != nil {
		t.Errorf("cache file does not exist: %v", err)
	}

	meta := f.run(t, "spec", "--meta")
	for _, want := range []string{"/openapi/v1.json", `"v1"`, "revalidated"} {
		if !strings.Contains(meta.stdout, want) {
			t.Errorf("meta output missing %q:\n%s", want, meta.stdout)
		}
	}
}

func TestSpecOfflineWithAColdCacheIsExitThree(t *testing.T) {
	srv := newSpecStub(t)
	f := specFixture(t, srv)

	got := f.run(t, "spec", "--offline")

	if got.code != output.ExitConfig {
		t.Errorf("exit = %d, want %d", got.code, output.ExitConfig)
	}
	if srv.requests != 0 {
		t.Errorf("requests = %d, want --offline to touch no network", srv.requests)
	}
	if !strings.Contains(got.stderr, "--offline") {
		t.Errorf("stderr = %q, want it to explain the cause", got.stderr)
	}
}

func TestSpecOfflineWithAWarmCacheWorks(t *testing.T) {
	srv := newSpecStub(t)
	f := specFixture(t, srv)

	if got := f.run(t, "spec"); got.code != output.ExitOK {
		t.Fatalf("warming the cache: %s", got.stderr)
	}
	before := srv.requests

	got := f.run(t, "spec", "--offline")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if srv.requests != before {
		t.Errorf("requests = %d, want no network", srv.requests-before)
	}
}

func TestSpecBackendDownWithAWarmCacheWarnsAndWorks(t *testing.T) {
	srv := newSpecStub(t)
	f := specFixture(t, srv)

	if got := f.run(t, "spec"); got.code != output.ExitOK {
		t.Fatalf("warming the cache: %s", got.stderr)
	}
	srv.Close()

	got := f.run(t, "spec")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if strings.TrimSpace(got.stdout) != fixtureSpec {
		t.Errorf("stdout = %q, want the cached spec", got.stdout)
	}
	if !strings.Contains(got.stderr, "using the cache") {
		t.Errorf("stderr = %q, want a warning", got.stderr)
	}
}

func TestSpecRefreshForcesAFullFetch(t *testing.T) {
	srv := newSpecStub(t)
	f := specFixture(t, srv)

	if got := f.run(t, "spec"); got.code != output.ExitOK {
		t.Fatalf("warming the cache: %s", got.stderr)
	}

	got := f.run(t, "spec", "--refresh")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if srv.conditions != 0 {
		t.Errorf("conditional requests = %d, want --refresh to send none", srv.conditions)
	}
}

func TestSpecRetriesWithAuthWhenTheSpecIsProtected(t *testing.T) {
	srv := newSpecStub(t)
	srv.protected = true
	f := specFixture(t, srv)

	got := f.run(t, "spec")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if strings.TrimSpace(got.stdout) != fixtureSpec {
		t.Errorf("stdout = %q, want the spec", got.stdout)
	}
}

func TestSpecWithNoSpecAnywhereIsExitThree(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	f := newFixture(t, "name = \"orders\"\n[env.dev]\nbase_url = \""+srv.URL+"\"\n")

	got := f.run(t, "spec")

	if got.code != output.ExitConfig {
		t.Errorf("exit = %d, want %d (%s)", got.code, output.ExitConfig, got.stderr)
	}
	if !strings.Contains(got.stderr, "spec_url") {
		t.Errorf("stderr = %q, want it to suggest the fix", got.stderr)
	}
}
