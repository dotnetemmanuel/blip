package creds

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotnetemmanuel/blip/internal/output"
)

func writeCreds(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.toml")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

const sample = `
[orders-dev]
type = "bearer"
token = "tok-literal"

[from-vault]
type = "bearer"
token_command = "printf 'tok-from-vault\n'"

[legacy]
type = "header"
header = "X-Api-Key"
value_command = "printf 'key-123'"

[interp]
type = "bearer"
token = "prefix-${BLIP_TEST_TOKEN}"

[svc]
type = "oauth2_cc"
token_url = "https://id.test/connect/token"
client_id = "blip"
client_secret_command = "printf 'shh'"
scope = "orders.read"
`

func TestLoadFileRefusesLoosePermissions(t *testing.T) {
	path := writeCreds(t, sample, 0o644)

	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("LoadFile succeeded on a 0644 file, want a refusal")
	}
	if !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("err = %q, want it to say how to fix the mode", err)
	}
	if got := output.ExitCodeFor(err); got != output.ExitAuth {
		t.Errorf("exit code = %d, want %d", got, output.ExitAuth)
	}
}

func TestLoadFileMissingIsATypedError(t *testing.T) {
	_, err := LoadFile(filepath.Join(t.TempDir(), "nope.toml"))
	if !errors.Is(err, ErrNoFile) {
		t.Fatalf("err = %v, want ErrNoFile", err)
	}
}

func TestLoadFileReadsProfiles(t *testing.T) {
	store, err := LoadFile(writeCreds(t, sample, 0o600))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	want := "from-vault,interp,legacy,orders-dev,svc"
	if got := strings.Join(store.Names(), ","); got != want {
		t.Errorf("Names = %q, want %q", got, want)
	}
}

func TestLoadFileRejects(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"no type", "[p]\ntoken = \"x\"\n", "has no type"},
		{"unknown type", "[p]\ntype = \"magic\"\n", "unknown type"},
		{"bearer with no token", "[p]\ntype = \"bearer\"\n", "needs token or token_command"},
		{"header with no name", "[p]\ntype = \"header\"\nvalue = \"x\"\n", "needs header"},
		{"header with no value", "[p]\ntype = \"header\"\nheader = \"X-K\"\n", "needs value or value_command"},
		{"basic with no password", "[p]\ntype = \"basic\"\nusername = \"u\"\n", "needs password"},
		{"oauth2 with no token_url", "[p]\ntype = \"oauth2_cc\"\nclient_id = \"c\"\nclient_secret = \"s\"\n", "needs token_url"},
		{"typo in a key", "[p]\ntype = \"bearer\"\ntoken_cmd = \"x\"\n", "strict"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFile(writeCreds(t, tt.body, 0o600))
			if err == nil {
				t.Fatal("LoadFile succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %q, want it to mention %q", err, tt.want)
			}
			if got := output.ExitCodeFor(err); got != output.ExitAuth {
				t.Errorf("exit code = %d, want %d", got, output.ExitAuth)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	t.Setenv("BLIP_TEST_TOKEN", "from-env")
	store, err := LoadFile(writeCreds(t, sample, 0o600))
	if err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(nil, nil)

	tests := []struct {
		profile string
		check   func(*testing.T, *Resolved)
	}{
		{"orders-dev", func(t *testing.T, r *Resolved) {
			if r.Token != "tok-literal" {
				t.Errorf("Token = %q, want tok-literal", r.Token)
			}
		}},
		{"from-vault", func(t *testing.T, r *Resolved) {
			if r.Token != "tok-from-vault" {
				t.Errorf("Token = %q, want the command output trimmed", r.Token)
			}
		}},
		{"legacy", func(t *testing.T, r *Resolved) {
			if r.Header != "X-Api-Key" || r.Value != "key-123" {
				t.Errorf("Header/Value = %q/%q, want X-Api-Key/key-123", r.Header, r.Value)
			}
		}},
		{"interp", func(t *testing.T, r *Resolved) {
			if r.Token != "prefix-from-env" {
				t.Errorf("Token = %q, want prefix-from-env", r.Token)
			}
		}},
		{"svc", func(t *testing.T, r *Resolved) {
			if r.ClientSecret != "shh" || r.Scope != "orders.read" {
				t.Errorf("ClientSecret/Scope = %q/%q, want shh/orders.read", r.ClientSecret, r.Scope)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.profile, func(t *testing.T) {
			got, err := store.Resolve(context.Background(), tt.profile, resolver)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			tt.check(t, got)
		})
	}
}

func TestResolveUnknownProfileListsWhatExists(t *testing.T) {
	store, err := LoadFile(writeCreds(t, sample, 0o600))
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Resolve(context.Background(), "nope", NewResolver(nil, nil))
	if err == nil {
		t.Fatal("Resolve succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "orders-dev") {
		t.Errorf("err = %q, want it to list the known profiles", err)
	}
}

func TestResolveFailures(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "command that fails",
			body: "[p]\ntype = \"bearer\"\ntoken_command = \"exit 3\"\n",
			want: "token_command failed",
		},
		{
			name: "command with no output",
			body: "[p]\ntype = \"bearer\"\ntoken_command = \"true\"\n",
			want: "produced no output",
		},
		{
			name: "literal and command together",
			body: "[p]\ntype = \"bearer\"\ntoken = \"x\"\ntoken_command = \"echo y\"\n",
			want: "pick one",
		},
		{
			name: "unset environment variable",
			body: "[p]\ntype = \"bearer\"\ntoken = \"${BLIP_DEFINITELY_UNSET}\"\n",
			want: "references unset BLIP_DEFINITELY_UNSET",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := LoadFile(writeCreds(t, tt.body, 0o600))
			if err != nil {
				t.Fatal(err)
			}

			_, err = store.Resolve(context.Background(), "p", NewResolver(nil, nil))
			if err == nil {
				t.Fatal("Resolve succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %q, want it to mention %q", err, tt.want)
			}
			if got := output.ExitCodeFor(err); got != output.ExitAuth {
				t.Errorf("exit code = %d, want %d", got, output.ExitAuth)
			}
		})
	}
}

func TestFailingCommandErrorDoesNotLeakStdout(t *testing.T) {
	body := "[p]\ntype = \"bearer\"\ntoken_command = \"printf 'super-secret'; exit 1\"\n"
	store, err := LoadFile(writeCreds(t, body, 0o600))
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Resolve(context.Background(), "p", NewResolver(nil, nil))
	if err == nil {
		t.Fatal("Resolve succeeded, want an error")
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Errorf("err = %q, want it to keep command stdout out of the message", err)
	}
}

func TestSecretValues(t *testing.T) {
	r := &Resolved{Token: "t", Value: "", Password: "p", ClientSecret: "cs", Username: "u"}

	got := strings.Join(r.SecretValues(), ",")
	if got != "t,p,cs" {
		t.Errorf("SecretValues = %q, want the non-empty secrets and nothing else", got)
	}
}
