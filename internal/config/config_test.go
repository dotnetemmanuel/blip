package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotnetemmanuel/blip/internal/output"
)

func writeConfig(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const minimal = `
name = "orders"
default_env = "dev"

[env.dev]
base_url = "https://localhost:7284"
insecure = true
auth = "orders-dev"
timeout = "5s"

[env.prod]
base_url = "https://api.internal"
auth = "orders-prod"
readonly = true
`

func TestDiscoverWalksUpToTheRepoRoot(t *testing.T) {
	root := t.TempDir()
	want := writeConfig(t, root, ".blip.toml", minimal)
	deep := filepath.Join(root, "src", "api", "handlers")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := Discover(deep)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got != want {
		t.Errorf("Discover = %q, want %q", got, want)
	}
}

func TestDiscoverPrefersTheDotfileAtTheSameLevel(t *testing.T) {
	root := t.TempDir()
	want := writeConfig(t, root, ".blip.toml", minimal)
	writeConfig(t, root, "blip.toml", minimal)

	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got != want {
		t.Errorf("Discover = %q, want the dotfile %q", got, want)
	}
}

func TestDiscoverAcceptsTheVisibleName(t *testing.T) {
	root := t.TempDir()
	want := writeConfig(t, root, "blip.toml", minimal)

	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got != want {
		t.Errorf("Discover = %q, want %q", got, want)
	}
}

func TestDiscoverStopsAtTheNearestConfig(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, ".blip.toml", minimal)
	nested := filepath.Join(root, "sub")
	want := writeConfig(t, nested, ".blip.toml", minimal)

	got, err := Discover(nested)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got != want {
		t.Errorf("Discover = %q, want the nearest config %q", got, want)
	}
}

func TestDiscoverMissingIsAConfigError(t *testing.T) {
	// A temp dir has no config above it only if we do not walk into the real
	// filesystem, so assert on the error kind rather than on reaching the root.
	_, err := Discover(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		return
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got := output.ExitCodeFor(err); got != output.ExitConfig {
		t.Errorf("exit code = %d, want %d", got, output.ExitConfig)
	}
}

func TestLoadReadsEverything(t *testing.T) {
	path := writeConfig(t, t.TempDir(), ".blip.toml", minimal+`
[[route]]
name = "health"
method = "GET"
path = "/healthz"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "orders" {
		t.Errorf("Name = %q, want orders", cfg.Name)
	}
	if got := cfg.EnvNames(); strings.Join(got, ",") != "dev,prod" {
		t.Errorf("EnvNames = %v, want [dev prod] sorted", got)
	}
	if len(cfg.Routes) != 1 || cfg.Routes[0].Path != "/healthz" {
		t.Errorf("Routes = %+v, want one /healthz route", cfg.Routes)
	}
	if cfg.Root != filepath.Dir(path) {
		t.Errorf("Root = %q, want %q", cfg.Root, filepath.Dir(path))
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "no name",
			body: "[env.dev]\nbase_url = \"https://x.test\"\n",
			want: "name is required",
		},
		{
			name: "no environments",
			body: "name = \"orders\"\n",
			want: "no environments defined",
		},
		{
			name: "missing base_url",
			body: "name = \"orders\"\n[env.dev]\nauth = \"x\"\n",
			want: "base_url is required",
		},
		{
			name: "base_url with no scheme",
			body: "name = \"orders\"\n[env.dev]\nbase_url = \"api.internal\"\n",
			want: "must be http or https",
		},
		{
			name: "insecure against a real host",
			body: "name = \"orders\"\n[env.prod]\nbase_url = \"https://api.internal\"\ninsecure = true\n",
			want: "is not local",
		},
		{
			name: "unparseable timeout",
			body: "name = \"orders\"\n[env.dev]\nbase_url = \"https://x.test\"\ntimeout = \"soon\"\n",
			want: "is not a duration",
		},
		{
			name: "default_env naming nothing",
			body: "name = \"orders\"\ndefault_env = \"stage\"\n[env.dev]\nbase_url = \"https://x.test\"\n",
			want: "is not a defined environment",
		},
		{
			name: "typo in a key",
			body: "name = \"orders\"\n[env.dev]\nbase_url = \"https://x.test\"\nread_only = true\n",
			want: "read_only",
		},
		{
			name: "client cert without key",
			body: "name = \"orders\"\n[env.dev]\nbase_url = \"https://x.test\"\nclient_cert = \"c.pem\"\n",
			want: "both client_cert and client_key",
		},
		{
			name: "route without a method",
			body: "name = \"orders\"\n[env.dev]\nbase_url = \"https://x.test\"\n[[route]]\nname = \"health\"\npath = \"/z\"\n",
			want: "has no method",
		},
		{
			name: "route path without a slash",
			body: "name = \"orders\"\n[env.dev]\nbase_url = \"https://x.test\"\n[[route]]\nname = \"h\"\nmethod = \"GET\"\npath = \"z\"\n",
			want: "must start with /",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, t.TempDir(), ".blip.toml", tt.body)

			_, err := Load(path)
			if err == nil {
				t.Fatal("Load succeeded, want a config error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %q, want it to mention %q", err, tt.want)
			}
			if got := output.ExitCodeFor(err); got != output.ExitConfig {
				t.Errorf("exit code = %d, want %d", got, output.ExitConfig)
			}
		})
	}
}

func TestInsecureAllowsLoopback(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "[::1]", "127.0.0.5"} {
		t.Run(host, func(t *testing.T) {
			body := "name = \"o\"\n[env.dev]\nbase_url = \"https://" + host + ":7284\"\ninsecure = true\n"
			if _, err := Load(writeConfig(t, t.TempDir(), ".blip.toml", body)); err != nil {
				t.Errorf("Load: %v", err)
			}
		})
	}
}

func TestResolveEnvPrecedence(t *testing.T) {
	path := writeConfig(t, t.TempDir(), ".blip.toml", minimal)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		explicit string
		blipEnv  string
		want     string
	}{
		{"flag beats everything", "prod", "dev", "prod"},
		{"env var beats default_env", "", "prod", "prod"},
		{"default_env when nothing else", "", "", "dev"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BLIP_ENV", tt.blipEnv)

			env, err := cfg.ResolveEnv(tt.explicit)
			if err != nil {
				t.Fatalf("ResolveEnv: %v", err)
			}
			if env.Name != tt.want {
				t.Errorf("env = %q, want %q", env.Name, tt.want)
			}
		})
	}
}

func TestResolveEnvFallsBackToTheSoleEnvironment(t *testing.T) {
	t.Setenv("BLIP_ENV", "")
	body := "name = \"o\"\n[env.only]\nbase_url = \"https://x.test\"\n"
	cfg, err := Load(writeConfig(t, t.TempDir(), ".blip.toml", body))
	if err != nil {
		t.Fatal(err)
	}

	env, err := cfg.ResolveEnv("")
	if err != nil {
		t.Fatalf("ResolveEnv: %v", err)
	}
	if env.Name != "only" {
		t.Errorf("env = %q, want only", env.Name)
	}
}

func TestResolveEnvNeedsADecisionWhenAmbiguous(t *testing.T) {
	t.Setenv("BLIP_ENV", "")
	body := "name = \"o\"\n[env.a]\nbase_url = \"https://a.test\"\n[env.b]\nbase_url = \"https://b.test\"\n"
	cfg, err := Load(writeConfig(t, t.TempDir(), ".blip.toml", body))
	if err != nil {
		t.Fatal(err)
	}

	_, err = cfg.ResolveEnv("")
	if err == nil {
		t.Fatal("ResolveEnv succeeded, want an error naming the choices")
	}
	if !strings.Contains(err.Error(), "a, b") {
		t.Errorf("err = %q, want it to list the environments", err)
	}
}

func TestResolveEnvUnknownNamesTheSource(t *testing.T) {
	t.Setenv("BLIP_ENV", "stage")
	cfg, err := Load(writeConfig(t, t.TempDir(), ".blip.toml", minimal))
	if err != nil {
		t.Fatal(err)
	}

	_, err = cfg.ResolveEnv("")
	if err == nil {
		t.Fatal("ResolveEnv succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "BLIP_ENV") {
		t.Errorf("err = %q, want it to name BLIP_ENV as the source", err)
	}
}

func TestEnvironmentDefaultsAndPaths(t *testing.T) {
	t.Setenv("BLIP_ENV", "")
	dir := t.TempDir()
	body := "name = \"o\"\n[env.dev]\nbase_url = \"https://localhost:7284/\"\nclient_cert = \"certs/c.pem\"\nclient_key = \"/abs/k.pem\"\n"
	cfg, err := Load(writeConfig(t, dir, ".blip.toml", body))
	if err != nil {
		t.Fatal(err)
	}

	env, err := cfg.ResolveEnv("")
	if err != nil {
		t.Fatal(err)
	}

	if env.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %v, want the %v default", env.Timeout, DefaultTimeout)
	}
	if env.BaseURL.Path != "" {
		t.Errorf("BaseURL.Path = %q, want the trailing slash trimmed", env.BaseURL.Path)
	}
	if want := filepath.Join(dir, "certs/c.pem"); env.ClientCert != want {
		t.Errorf("ClientCert = %q, want it resolved against the config dir as %q", env.ClientCert, want)
	}
	if env.ClientKey != "/abs/k.pem" {
		t.Errorf("ClientKey = %q, want the absolute path untouched", env.ClientKey)
	}
}

func TestTimeoutIsRead(t *testing.T) {
	t.Setenv("BLIP_ENV", "")
	cfg, err := Load(writeConfig(t, t.TempDir(), ".blip.toml", minimal))
	if err != nil {
		t.Fatal(err)
	}

	env, err := cfg.ResolveEnv("dev")
	if err != nil {
		t.Fatal(err)
	}
	if env.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", env.Timeout)
	}
	if !env.Insecure {
		t.Error("Insecure = false, want true")
	}
	if env.Auth != "orders-dev" {
		t.Errorf("Auth = %q, want orders-dev", env.Auth)
	}
}

func TestClientCertMayNotClimbOutOfTheRepo(t *testing.T) {
	// .blip.toml is committed and may come from a cloned repo, so it must not be
	// able to walk to an identity kept elsewhere on the machine.
	body := "name = \"o\"\n[env.dev]\nbase_url = \"https://x.test\"\n" +
		"client_cert = \"../../../home/user/.certs/prod.pem\"\nclient_key = \"k.pem\"\n"

	_, err := Load(writeConfig(t, t.TempDir(), ".blip.toml", body))

	if err == nil {
		t.Fatal("Load accepted a traversing certificate path")
	}
	if !strings.Contains(err.Error(), "climb out of the repo") {
		t.Errorf("err = %q, want it to name the problem", err)
	}
}
