package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dotnetemmanuel/blip/internal/output"
)

// run keeps config discovery, the credential store and the cache inside the
// test's own directories. Without that, a .blip.toml anywhere above the repo
// would make these tests read real credentials and make real requests.
func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("BLIP_ENV", "")

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rt := &Runtime{Globals: &Globals{}, Stdout: out, Stderr: errOut, Dir: dir}
	rt.Globals.prescan(args)
	code = Run(rt, args)
	return code, out.String(), errOut.String()
}

func TestVersionCommandPrintsVersion(t *testing.T) {
	code, stdout, stderr := run(t, "version")

	if code != output.ExitOK {
		t.Errorf("exit code = %d, want %d", code, output.ExitOK)
	}
	if want := "blip " + Version() + " "; !strings.HasPrefix(stdout, want) {
		t.Errorf("stdout = %q, want it to start with %q", stdout, want)
	}
	if !strings.HasSuffix(stdout, "\n") {
		t.Errorf("stdout = %q, want a trailing newline", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestVersionIsNeverEmpty(t *testing.T) {
	if Version() == "" {
		t.Error("Version() is empty; the build must always report something")
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"version", "--bogus"}},
		{"surplus argument", []string{"version", "extra"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := run(t, tt.args...)

			if code != output.ExitUsage {
				t.Errorf("exit code = %d, want %d", code, output.ExitUsage)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty; diagnostics belong on stderr", stdout)
			}
			if stderr == "" {
				t.Error("stderr is empty, want an explanation of the usage error")
			}
		})
	}
}

func TestHelpGoesToStdoutAndExitsZero(t *testing.T) {
	code, stdout, _ := run(t, "--help")

	if code != output.ExitOK {
		t.Errorf("exit code = %d, want %d", code, output.ExitOK)
	}
	if !strings.Contains(stdout, "blip") {
		t.Errorf("stdout = %q, want the help text", stdout)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v0.2.0", "v0.2.1", -1},
		{"v0.2.1", "v0.2.1", 0},
		{"v0.3.0", "v0.2.9", 1},
		{"v1.0.0", "v0.9.9", 1},
		{"v0.2.1", "0.2.1", 0},
		{"v0.2.1-rc1", "v0.2.1", 0},
		// A dirty build of the newest tag is still the newest release.
		{"v0.2.1-dirty", "v0.2.1", 0},
		// An unstamped development build really is behind every release.
		{"0.0.0-dev", "v0.2.1", -1},
		// Nothing to say when a version cannot be read at all.
		{"v0.2.1", "nonsense", 0},
		{"", "v0.2.1", 0},
	}

	for _, tt := range tests {
		if got := compareVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// releaseStub stands in for the GitHub releases API.
func releaseStub(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	previous := releaseAPI
	releaseAPI = srv.URL
	t.Cleanup(func() { releaseAPI = previous })
}

func TestVersionCheckReportsANewerRelease(t *testing.T) {
	releaseStub(t, http.StatusOK, `{"tag_name":"v99.0.0"}`)

	code, stdout, _ := run(t, "version", "--check")

	if code != output.ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "a newer release is available: v99.0.0") {
		t.Errorf("stdout = %q, want the newer version named", stdout)
	}
	if !strings.Contains(stdout, "curl -fsSL") {
		t.Errorf("stdout = %q, want the command to run", stdout)
	}
	if !strings.Contains(stdout, runtime.GOOS+"-"+runtime.GOARCH) {
		t.Errorf("stdout = %q, want the binary for this machine", stdout)
	}
}

func TestVersionCheckIsQuietWhenCurrent(t *testing.T) {
	releaseStub(t, http.StatusOK, `{"tag_name":"`+Version()+`"}`)

	code, stdout, _ := run(t, "version", "--check")

	if code != output.ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "is the newest release") {
		t.Errorf("stdout = %q, want it to say there is nothing to do", stdout)
	}
	if strings.Contains(stdout, "curl") {
		t.Errorf("stdout = %q, want no upgrade command when there is nothing to upgrade", stdout)
	}
}

func TestVersionWithoutCheckTouchesNoNetwork(t *testing.T) {
	asked := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked = true
		_, _ = w.Write([]byte(`{"tag_name":"v99.0.0"}`))
	}))
	defer srv.Close()
	previous := releaseAPI
	releaseAPI = srv.URL
	defer func() { releaseAPI = previous }()

	if code, _, _ := run(t, "version"); code != output.ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if asked {
		t.Error("plain version reached the network")
	}
}

func TestVersionCheckFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"rate limited", http.StatusForbidden, `{}`, output.ExitAuth},
		{"not found", http.StatusNotFound, `{}`, output.ExitClient},
		{"no version named", http.StatusOK, `{}`, output.ExitTransport},
		{"not json", http.StatusOK, `<html>`, output.ExitTransport},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			releaseStub(t, tt.status, tt.body)

			code, stdout, _ := run(t, "version", "--check")

			if code != tt.want {
				t.Errorf("exit = %d, want %d", code, tt.want)
			}
			// The version itself printed before the lookup was attempted.
			if !strings.HasPrefix(stdout, "blip ") {
				t.Errorf("stdout = %q, want the version line regardless", stdout)
			}
		})
	}
}

func TestVersionCheckRespectsOffline(t *testing.T) {
	asked := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked = true
		_, _ = w.Write([]byte(`{"tag_name":"v99.0.0"}`))
	}))
	defer srv.Close()
	previous := releaseAPI
	releaseAPI = srv.URL
	defer func() { releaseAPI = previous }()

	code, _, stderr := run(t, "version", "--check", "--offline")

	if code != output.ExitConfig {
		t.Errorf("exit = %d, want %d", code, output.ExitConfig)
	}
	if asked {
		t.Error("--offline still reached the network")
	}
	if !strings.Contains(stderr, "offline") {
		t.Errorf("stderr = %q, want it to explain", stderr)
	}
}
