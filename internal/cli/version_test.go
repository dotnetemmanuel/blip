package cli

import (
	"bytes"
	"path/filepath"
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
