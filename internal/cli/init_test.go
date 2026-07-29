package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotnetemmanuel/blip/internal/config"
	"github.com/dotnetemmanuel/blip/internal/output"
)

// initFixture is a repo with no blip config at all, which is the only state
// init is ever run in.
func initFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "confighome"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cachehome"))
	t.Setenv("BLIP_ENV", "")
	return &fixture{dir: dir}
}

func specServer(t *testing.T, path string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openapi":"3.0.1","info":{"title":"T","version":"1"},"paths":{}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestInitWritesAUsableConfig(t *testing.T) {
	f := initFixture(t)
	srv := specServer(t, "/openapi/v1.json")

	got := f.run(t, "init", srv.URL, "--auth", "myapi-dev")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}

	// The real test of a scaffold is that the tool it scaffolds for accepts it.
	cfg, err := config.Load(filepath.Join(f.dir, ".blip.toml"))
	if err != nil {
		t.Fatalf("the config init wrote does not load: %v", err)
	}
	env, err := cfg.ResolveEnv("")
	if err != nil {
		t.Fatalf("ResolveEnv: %v", err)
	}
	if env.Name != "dev" {
		t.Errorf("env = %q, want dev", env.Name)
	}
	if env.BaseURL.String() != srv.URL {
		t.Errorf("base_url = %q, want %q", env.BaseURL, srv.URL)
	}
	if env.Auth != "myapi-dev" {
		t.Errorf("auth = %q, want myapi-dev", env.Auth)
	}
	if cfg.Name != filepath.Base(f.dir) {
		t.Errorf("name = %q, want the repo directory name", cfg.Name)
	}
}

func TestInitRecordsOnlyANonStandardSpecPath(t *testing.T) {
	tests := []struct {
		name      string
		served    string
		wantSpec  string
		wantFound bool
	}{
		{"standard path is not recorded", "/openapi/v1.json", "", true},
		{"swashbuckle path is not recorded", "/swagger/v1/swagger.json", "", true},
		{"no spec at all", "/nowhere", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := initFixture(t)
			srv := specServer(t, tt.served)

			got := f.run(t, "init", srv.URL)

			if got.code != output.ExitOK {
				t.Fatalf("exit = %d (%s)", got.code, got.stderr)
			}
			body, err := os.ReadFile(filepath.Join(f.dir, ".blip.toml"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "spec_url") != (tt.wantSpec != "") {
				t.Errorf("config = %q, spec_url presence is wrong", body)
			}
			if strings.Contains(got.stderr, "found a spec") != tt.wantFound {
				t.Errorf("stderr = %q, want found=%v", got.stderr, tt.wantFound)
			}
		})
	}
}

func TestInitRecordsANonStandardSpecPath(t *testing.T) {
	f := initFixture(t)
	// Springdoc serves here, and blip does not probe it, so it has to be recorded
	// or every later run would fail to find the spec.
	srv := specServer(t, "/v3/api-docs")

	got := f.run(t, "init", srv.URL)

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	body, err := os.ReadFile(filepath.Join(f.dir, ".blip.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `spec_url = "/v3/api-docs"`) {
		t.Errorf("config = %q, want the non-standard path recorded", body)
	}
	cfg, err := config.Load(filepath.Join(f.dir, ".blip.toml"))
	if err != nil {
		t.Fatalf("the config init wrote does not load: %v", err)
	}
	env, err := cfg.ResolveEnv("")
	if err != nil {
		t.Fatal(err)
	}
	if env.SpecURL != "/v3/api-docs" {
		t.Errorf("SpecURL = %q, want /v3/api-docs", env.SpecURL)
	}
}

func TestInitMarksLocalhostInsecure(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"https://localhost:7161", true},
		{"https://127.0.0.1:7161", true},
		{"https://api.example.internal", false},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			f := initFixture(t)

			got := f.run(t, "init", tt.host)

			if got.code != output.ExitOK {
				t.Fatalf("exit = %d (%s)", got.code, got.stderr)
			}
			body, err := os.ReadFile(filepath.Join(f.dir, ".blip.toml"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "insecure") != tt.want {
				t.Errorf("config = %q, want insecure=%v", body, tt.want)
			}
			// Whatever it writes has to be a config blip will accept.
			if _, err := config.Load(filepath.Join(f.dir, ".blip.toml")); err != nil {
				t.Errorf("the config init wrote does not load: %v", err)
			}
		})
	}
}

func TestInitWritesTheClaudeFiles(t *testing.T) {
	f := initFixture(t)
	srv := specServer(t, "/openapi/v1.json")

	f.run(t, "init", srv.URL)

	note, err := os.ReadFile(filepath.Join(f.dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("no CLAUDE.md: %v", err)
	}
	for _, want := range []string{"describe --compact", "--dry-run", "--yes", "Never pass"} {
		if !strings.Contains(string(note), want) {
			t.Errorf("CLAUDE.md is missing %q", want)
		}
	}

	settings, err := os.ReadFile(filepath.Join(f.dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("no settings.json: %v", err)
	}
	if !strings.Contains(string(settings), PermissionRule) {
		t.Errorf("settings.json = %q, want the permission rule", settings)
	}
}

func TestInitNeverOverwritesSomeoneElsesFiles(t *testing.T) {
	f := initFixture(t)
	srv := specServer(t, "/openapi/v1.json")

	claudeMd := filepath.Join(f.dir, "CLAUDE.md")
	settings := filepath.Join(f.dir, ".claude", "settings.json")
	if err := os.WriteFile(claudeMd, []byte("# House rules\n\nKeep it tidy.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{"permissions":{"allow":["Bash(make *)"]},"theme":"dark"}`
	if err := os.WriteFile(settings, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	got := f.run(t, "init", srv.URL)

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}

	// An existing settings.json is someone's configuration. Report, do not merge.
	after, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Errorf("settings.json was rewritten:\n%s", after)
	}
	if !strings.Contains(got.stderr, "add") {
		t.Errorf("stderr = %q, want it to say what to add by hand", got.stderr)
	}

	// CLAUDE.md is append-only, so the existing content has to survive.
	note, err := os.ReadFile(claudeMd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(note), "House rules") {
		t.Error("the existing CLAUDE.md content was lost")
	}
	if !strings.Contains(string(note), "blip") {
		t.Error("the blip section was not appended")
	}
}

func TestInitSaysNothingTwice(t *testing.T) {
	f := initFixture(t)
	srv := specServer(t, "/openapi/v1.json")

	f.run(t, "init", srv.URL)
	first, err := os.ReadFile(filepath.Join(f.dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}

	got := f.run(t, "init", srv.URL, "--force")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	second, err := os.ReadFile(filepath.Join(f.dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Errorf("CLAUDE.md gained a second copy of the note:\n%s", second)
	}
}

func TestInitRefusesToClobberAnExistingConfig(t *testing.T) {
	f := initFixture(t)
	srv := specServer(t, "/openapi/v1.json")
	existing := filepath.Join(f.dir, ".blip.toml")
	if err := os.WriteFile(existing, []byte("name = \"mine\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := f.run(t, "init", srv.URL)

	if got.code != output.ExitUsage {
		t.Errorf("exit = %d, want %d", got.code, output.ExitUsage)
	}
	body, _ := os.ReadFile(existing)
	if string(body) != "name = \"mine\"\n" {
		t.Errorf("the existing config was replaced: %q", body)
	}
	if !strings.Contains(got.stderr, "--force") {
		t.Errorf("stderr = %q, want it to name the way through", got.stderr)
	}
}

func TestInitWritesAtTheRepoRoot(t *testing.T) {
	f := initFixture(t)
	srv := specServer(t, "/openapi/v1.json")
	deep := filepath.Join(f.dir, "src", "api")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	from := &fixture{dir: deep}

	got := from.run(t, "init", srv.URL)

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if _, err := os.Stat(filepath.Join(f.dir, ".blip.toml")); err != nil {
		t.Errorf("config was not written beside .git: %v", err)
	}
	if _, err := os.Stat(filepath.Join(deep, ".blip.toml")); err == nil {
		t.Error("config was written in the subdirectory instead of the repo root")
	}
}

func TestInitDryRunWritesNothing(t *testing.T) {
	f := initFixture(t)
	srv := specServer(t, "/openapi/v1.json")

	got := f.run(t, "init", srv.URL, "--dry-run")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "base_url") {
		t.Errorf("stdout = %q, want a preview of the config", got.stdout)
	}
	for _, name := range []string{".blip.toml", "CLAUDE.md", ".claude/settings.json"} {
		if _, err := os.Stat(filepath.Join(f.dir, name)); err == nil {
			t.Errorf("--dry-run wrote %s", name)
		}
	}
}

func TestInitNoClaudeSkipsTheAgentFiles(t *testing.T) {
	f := initFixture(t)
	srv := specServer(t, "/openapi/v1.json")

	got := f.run(t, "init", srv.URL, "--claude=false")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if _, err := os.Stat(filepath.Join(f.dir, ".blip.toml")); err != nil {
		t.Errorf("config was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "CLAUDE.md")); err == nil {
		t.Error("--claude=false still wrote CLAUDE.md")
	}
}

func TestInitRejectsANonURL(t *testing.T) {
	for _, arg := range []string{"localhost:7161", "ftp://x.test", "/just/a/path", "https://"} {
		t.Run(arg, func(t *testing.T) {
			f := initFixture(t)

			got := f.run(t, "init", arg)

			if got.code != output.ExitUsage {
				t.Errorf("exit = %d, want %d", got.code, output.ExitUsage)
			}
		})
	}
}

func TestInitRefusesCredentialsInTheBaseURL(t *testing.T) {
	// base_url is committed, and probing would put these on the wire first.
	for _, raw := range []string{
		"http://admin:hunter2@127.0.0.1:9/base",
		"http://127.0.0.1:9/base?api_key=SEKRET",
		"http://127.0.0.1:9/base#tok",
	} {
		t.Run(raw, func(t *testing.T) {
			f := initFixture(t)

			got := f.run(t, "init", raw)

			if got.code != output.ExitUsage {
				t.Errorf("exit = %d, want %d", got.code, output.ExitUsage)
			}
			if _, err := os.Stat(filepath.Join(f.dir, ".blip.toml")); err == nil {
				body, _ := os.ReadFile(filepath.Join(f.dir, ".blip.toml"))
				t.Errorf("a config was written anyway:\n%s", body)
			}
		})
	}
}

func TestInitDryRunTouchesNoNetwork(t *testing.T) {
	f := initFixture(t)
	probes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		probes++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	got := f.run(t, "init", srv.URL, "--dry-run")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if probes != 0 {
		t.Errorf("probes = %d, want a dry run to send nothing", probes)
	}
}

func TestInitRefusesToWriteThroughASymlink(t *testing.T) {
	tests := []struct {
		name string
		link string
	}{
		{"CLAUDE.md", "CLAUDE.md"},
		{"the claude directory", ".claude"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := initFixture(t)
			srv := specServer(t, "/openapi/v1.json")
			outside := filepath.Join(t.TempDir(), "victim")
			if err := os.MkdirAll(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(outside, "target")
			if err := os.WriteFile(target, []byte("original\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(f.dir, tt.link)); err != nil {
				t.Fatal(err)
			}

			f.run(t, "init", srv.URL)

			body, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != "original\n" {
				t.Errorf("a file outside the repo was written through the symlink:\n%s", body)
			}
		})
	}
}

func TestInitStopsAtTheHomeDirectory(t *testing.T) {
	// A stray run in a dotfiles repo must not scaffold into the machine-wide
	// .claude/settings.json.
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(home, "scratch")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "confighome"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cachehome"))
	t.Setenv("BLIP_ENV", "")
	f := &fixture{dir: work}

	f.run(t, "init", "https://127.0.0.1:9")

	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err == nil {
		t.Error("init granted the permission machine-wide by writing into the home directory")
	}
	if _, err := os.Stat(filepath.Join(work, ".blip.toml")); err != nil {
		t.Errorf("nothing was written where the user was standing: %v", err)
	}
}

func TestInitReportsAWriteFailure(t *testing.T) {
	f := initFixture(t)
	srv := specServer(t, "/openapi/v1.json")
	locked := filepath.Join(f.dir, ".claude")
	if err := os.MkdirAll(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	got := f.run(t, "init", srv.URL)

	if got.code == output.ExitOK {
		t.Errorf("exit = 0 after failing to write the permission rule (%s)", got.stderr)
	}
}

func TestInitDoesNotCallHTTPInsecure(t *testing.T) {
	f := initFixture(t)

	got := f.run(t, "init", "http://127.0.0.1:9")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	body, err := os.ReadFile(filepath.Join(f.dir, ".blip.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "insecure") {
		t.Errorf("config = %q, want no insecure line where there is no TLS", body)
	}
}

func TestInitNoClaudeFlagExists(t *testing.T) {
	// The README, the long help and the examples all name this spelling.
	f := initFixture(t)
	srv := specServer(t, "/openapi/v1.json")

	got := f.run(t, "init", srv.URL, "--no-claude")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "CLAUDE.md")); err == nil {
		t.Error("--no-claude still wrote CLAUDE.md")
	}
}

func TestInitWarnsAboutTheDangerousFlags(t *testing.T) {
	f := initFixture(t)
	srv := specServer(t, "/openapi/v1.json")

	f.run(t, "init", srv.URL)

	note, err := os.ReadFile(filepath.Join(f.dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	// --config and --profile choose the host and the credential, so an agent has
	// to be told about them, not only about --env.
	for _, want := range []string{"--env", "--config", "--profile"} {
		if !strings.Contains(string(note), want) {
			t.Errorf("the note does not mention %q", want)
		}
	}
}
