package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotnetemmanuel/blip/internal/build"
	"github.com/dotnetemmanuel/blip/internal/output"
)

type result struct {
	code   int
	stdout string
	stderr string
}

// fixture is a repo with a .blip.toml and an isolated config and cache home.
type fixture struct {
	dir string
}

func newFixture(t *testing.T, blipToml string) *fixture {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".blip.toml"), []byte(blipToml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "confighome"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cachehome"))
	t.Setenv("BLIP_ENV", "")
	return &fixture{dir: dir}
}

func (f *fixture) writeCredentials(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "blip")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) run(t *testing.T, args ...string) result {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rt := &Runtime{
		Globals: &Globals{},
		Stdout:  stdout,
		Stderr:  stderr,
		Dir:     f.dir,
	}
	rt.Globals.prescan(args)
	code := Run(rt, args)
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

const twoEnvs = `
name = "orders"
default_env = "dev"

[env.dev]
base_url = "https://localhost:7284"
insecure = true
auth = "orders-dev"

[env.prod]
base_url = "https://api.internal"
auth = "orders-prod"
readonly = true
`

func TestEnvsListsEverything(t *testing.T) {
	f := newFixture(t, twoEnvs)

	got := f.run(t, "envs")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	for _, want := range []string{"dev", "https://localhost:7284", "orders-dev", "prod", "readonly", "insecure", "default"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, got.stdout)
		}
	}
	if !strings.Contains(got.stdout, "* dev") {
		t.Errorf("stdout does not mark the selected environment:\n%s", got.stdout)
	}
}

func TestEnvsJSON(t *testing.T) {
	f := newFixture(t, twoEnvs)

	got := f.run(t, "envs", "--json")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	var listings []envListing
	if err := json.Unmarshal([]byte(got.stdout), &listings); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, got.stdout)
	}
	if len(listings) != 2 {
		t.Fatalf("got %d environments, want 2", len(listings))
	}
	if listings[0].Name != "dev" || !listings[0].Selected || !listings[0].Insecure {
		t.Errorf("dev = %+v, want it selected and insecure", listings[0])
	}
	if !listings[1].Readonly {
		t.Errorf("prod = %+v, want readonly", listings[1])
	}
}

func TestEnvsSelectionFollowsTheFlag(t *testing.T) {
	f := newFixture(t, twoEnvs)

	got := f.run(t, "envs", "--env", "prod", "--json")

	var listings []envListing
	if err := json.Unmarshal([]byte(got.stdout), &listings); err != nil {
		t.Fatal(err)
	}
	if listings[0].Selected || !listings[1].Selected {
		t.Errorf("selection = %v/%v, want prod selected", listings[0].Selected, listings[1].Selected)
	}
}

func TestMissingConfigIsExitThree(t *testing.T) {
	f := &fixture{dir: t.TempDir()}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BLIP_ENV", "")

	got := f.run(t, "envs", "--config", filepath.Join(f.dir, "nothing.toml"))

	if got.code != output.ExitConfig {
		t.Errorf("exit = %d, want %d", got.code, output.ExitConfig)
	}
	if got.stdout != "" {
		t.Errorf("stdout = %q, want empty", got.stdout)
	}
}

func TestUnknownEnvIsExitThree(t *testing.T) {
	f := newFixture(t, twoEnvs)

	got := f.run(t, "auth", "test", "--env", "stage")

	if got.code != output.ExitConfig {
		t.Errorf("exit = %d, want %d", got.code, output.ExitConfig)
	}
	if !strings.Contains(got.stderr, "dev, prod") {
		t.Errorf("stderr = %q, want it to list the defined environments", got.stderr)
	}
}

func TestInsecureAgainstARealHostIsExitThree(t *testing.T) {
	f := newFixture(t, `
name = "orders"
[env.prod]
base_url = "https://api.internal"
insecure = true
`)

	got := f.run(t, "envs")

	if got.code != output.ExitConfig {
		t.Errorf("exit = %d, want %d", got.code, output.ExitConfig)
	}
	if !strings.Contains(got.stderr, "not local") {
		t.Errorf("stderr = %q, want it to explain the localhost rule", got.stderr)
	}
}

func TestAuthTestResolvesACommandWithoutPrintingTheToken(t *testing.T) {
	f := newFixture(t, twoEnvs)
	f.writeCredentials(t, `
[orders-dev]
type = "bearer"
token_command = "printf 'super-secret-token'"
`)

	got := f.run(t, "auth", "test")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if strings.Contains(got.stdout+got.stderr, "super-secret-token") {
		t.Errorf("output leaked the token:\n%s%s", got.stdout, got.stderr)
	}
	for _, want := range []string{"orders-dev", "bearer", "sha256:"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, got.stdout)
		}
	}
}

func TestAuthTestWithNoCredentialsFileIsExitFour(t *testing.T) {
	f := newFixture(t, twoEnvs)

	got := f.run(t, "auth", "test")

	if got.code != output.ExitAuth {
		t.Errorf("exit = %d, want %d", got.code, output.ExitAuth)
	}
	if !strings.Contains(got.stderr, "credentials file") {
		t.Errorf("stderr = %q, want it to name the missing file", got.stderr)
	}
}

func TestAuthTestRefusesALoosePermissionFile(t *testing.T) {
	f := newFixture(t, twoEnvs)
	f.writeCredentials(t, "[orders-dev]\ntype = \"bearer\"\ntoken = \"x\"\n")
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "blip", "credentials.toml")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	got := f.run(t, "auth", "test")

	if got.code != output.ExitAuth {
		t.Errorf("exit = %d, want %d", got.code, output.ExitAuth)
	}
	if !strings.Contains(got.stderr, "chmod 600") {
		t.Errorf("stderr = %q, want the fix spelled out", got.stderr)
	}
}

func TestProfileFlagOverridesTheEnvironment(t *testing.T) {
	f := newFixture(t, twoEnvs)
	f.writeCredentials(t, `
[orders-dev]
type = "bearer"
token = "dev-token"

[other]
type = "header"
header = "X-Api-Key"
value = "other-value"
`)

	got := f.run(t, "auth", "test", "--profile", "other")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "other") || !strings.Contains(got.stdout, "X-Api-Key") {
		t.Errorf("stdout = %q, want the overriding profile", got.stdout)
	}
}

func TestEnvWithNoAuthResolvesToNone(t *testing.T) {
	f := newFixture(t, "name = \"o\"\n[env.dev]\nbase_url = \"https://x.test\"\n")

	got := f.run(t, "auth", "test")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "none") {
		t.Errorf("stdout = %q, want it to report no auth", got.stdout)
	}
}

func TestPrescan(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want Globals
	}{
		{"space separated", []string{"envs", "--env", "prod"}, Globals{Env: "prod"}},
		{"equals form", []string{"envs", "--env=prod"}, Globals{Env: "prod"}},
		{"config path", []string{"--config", "/tmp/x.toml", "envs"}, Globals{ConfigPath: "/tmp/x.toml"}},
		{"profile", []string{"raw", "GET", "/x", "--profile=p"}, Globals{Profile: "p"}},
		{"bare booleans", []string{"spec", "--refresh", "--offline"}, Globals{Refresh: true, Offline: true}},
		{"explicit false", []string{"spec", "--offline=false"}, Globals{}},
		{"after a double dash", []string{"raw", "GET", "/x", "--", "--env", "prod"}, Globals{}},
		{"unrelated flags ignored", []string{"envs", "--json", "-v"}, Globals{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Globals
			got.prescan(tt.args)

			if got != tt.want {
				t.Errorf("prescan(%v) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}

func TestGlobalFlagsAreRegistered(t *testing.T) {
	rt := &Runtime{Globals: &Globals{}, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	flags := NewRootCommand(rt).PersistentFlags()

	for _, name := range []string{
		"env", "profile", "config", "refresh", "offline", "timeout",
		"output", "include-headers", "verbose", "dry-run", "yes", "strict",
	} {
		if flags.Lookup(name) == nil {
			t.Errorf("global flag --%s is not registered", name)
		}
	}
}

const routeOnly = `
name = "orders"
[env.dev]
base_url = "https://localhost:9"
[[route]]
name = "health"
method = "GET"
path = "/healthz"
`

func TestUnknownCommandInAConfiguredRepoIsExitTwo(t *testing.T) {
	f := newFixture(t, routeOnly)

	got := f.run(t, "nope", "--offline")

	if got.code != output.ExitUsage {
		t.Errorf("exit = %d, want %d (%s)", got.code, output.ExitUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "unknown command") {
		t.Errorf("stderr = %q, want it to say the command is unknown", got.stderr)
	}
}

func TestUnknownCommandWithNoConfigReportsTheConfigProblem(t *testing.T) {
	f := &fixture{dir: t.TempDir()}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("BLIP_ENV", "")

	got := f.run(t, "orders", "get", "1")

	if got.code != output.ExitConfig {
		t.Errorf("exit = %d, want %d", got.code, output.ExitConfig)
	}
	if !strings.Contains(got.stderr, ".blip.toml") {
		t.Errorf("stderr = %q, want the real problem named", got.stderr)
	}
}

// A spec tag must never be able to shadow one of blip's own commands, so the
// reserved list has to keep up with whatever the root registers.
func TestEveryRootCommandIsReserved(t *testing.T) {
	rt := &Runtime{Globals: &Globals{}, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}

	for _, cmd := range NewRootCommand(rt).Commands() {
		if !build.Reserved[cmd.Name()] {
			t.Errorf("command %q is not in build.Reserved, so a spec tag could shadow it", cmd.Name())
		}
	}
	// call and describe are attached only when a spec loads, so pin them too.
	for _, name := range []string{"call", "describe"} {
		if !build.Reserved[name] {
			t.Errorf("%q is not reserved", name)
		}
	}
}

func TestSpeclessCommandsAllExist(t *testing.T) {
	rt := &Runtime{Globals: &Globals{}, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}

	registered := map[string]bool{}
	for _, cmd := range NewRootCommand(rt).Commands() {
		registered[cmd.Name()] = true
	}
	for name := range specless {
		if name == "completion" || name == "help" {
			continue
		}
		if !registered[name] {
			t.Errorf("%q is listed as specless but is not a command", name)
		}
	}
}

// A cloned repo chooses base_url and names a profile. Without pinning it also
// chooses where your credential goes, which is the whole attack.
func TestAProfileCanBePinnedToItsHosts(t *testing.T) {
	srv := newStub(t)
	f := liveFixture(t, srv, "")
	f.writeCredentials(t, `
[orders-dev]
type  = "bearer"
token = "super-secret-token"
hosts = ["api.example.internal"]
`)

	got := f.run(t, "raw", "GET", "/healthz")

	if got.code != output.ExitBlocked {
		t.Errorf("exit = %d, want %d (%s)", got.code, output.ExitBlocked, got.stderr)
	}
	if srv.last.Method != "" {
		t.Error("the credential was sent to a host its profile does not name")
	}
	if !strings.Contains(got.stderr, "hosts") {
		t.Errorf("stderr = %q, want it to name the fix", got.stderr)
	}
}

func TestAPinnedProfileStillReachesItsOwnHost(t *testing.T) {
	srv := newStub(t)
	f := liveFixture(t, srv, "")
	host := strings.TrimPrefix(srv.URL, "http://")
	f.writeCredentials(t, `
[orders-dev]
type  = "bearer"
token = "super-secret-token"
hosts = ["`+host+`"]
`)

	got := f.run(t, "raw", "GET", "/healthz")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if srv.last.Header.Get("Authorization") != "Bearer super-secret-token" {
		t.Error("the credential was withheld from the host its profile names")
	}
}

func TestAnUnpinnedProfileIsRefusedForARemoteHost(t *testing.T) {
	// The cloned-repo attack: .blip.toml names the host and the profile, so an
	// unpinned credential must not travel anywhere a repo points it.
	f := newFixture(t, `
name = "orders"
[env.dev]
base_url = "https://api.example.internal"
auth = "orders-dev"
[[route]]
name = "health"
method = "GET"
path = "/healthz"
`)
	f.writeCredentials(t, bearerCreds)

	got := f.run(t, "health", "--dry-run")

	if got.code != output.ExitBlocked {
		t.Errorf("exit = %d, want %d (%s)", got.code, output.ExitBlocked, got.stderr)
	}
	for _, want := range []string{"hosts = ", "api.example.internal", "orders-dev"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", got.stderr, want)
		}
	}
}

func TestAnUnpinnedProfileIsQuietForLocalhost(t *testing.T) {
	srv := newStub(t)
	f := liveFixture(t, srv, "")

	got := f.run(t, "raw", "GET", "/healthz")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if strings.Contains(got.stderr, "hosts") {
		t.Errorf("stderr = %q, want no ceremony for a loopback host", got.stderr)
	}
}

func TestSpeclessCommandsSurviveASeparatedGlobalFlag(t *testing.T) {
	// --env dev used to be read as the subcommand "dev", so every one of these
	// built the tree, fetched a spec and could reach for the vault.
	f := newFixture(t, `
name = "orders"
[env.dev]
base_url = "https://127.0.0.1:9"
auth = "orders-dev"
`)
	f.writeCredentials(t, bearerCreds)

	for _, args := range [][]string{
		{"version"},
		{"--env", "dev", "version"},
		{"--profile", "orders-dev", "version"},
		{"--env", "dev", "envs"},
		{"--timeout", "5s", "envs"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			got := f.run(t, args...)
			if got.code != output.ExitOK {
				t.Errorf("exit = %d, want it to work without a spec (%s)", got.code, got.stderr)
			}
		})
	}
}

func TestHelpWorksWhenTheSpecCannotLoad(t *testing.T) {
	f := newFixture(t, "name = \"o\"\n[env.dev]\nbase_url = \"https://127.0.0.1:9\"\n")

	for _, args := range [][]string{
		{"describe", "--help"},
		{"--help"},
		{"raw", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			got := f.run(t, args...)
			if got.code != output.ExitOK {
				t.Errorf("exit = %d, want help to print anyway (%s)", got.code, got.stderr)
			}
			if !strings.Contains(got.stdout, "Usage:") {
				t.Errorf("stdout = %q, want the help text", got.stdout)
			}
		})
	}
}
