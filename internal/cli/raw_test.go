package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotnetemmanuel/blip/internal/output"
)

// capture is what the stub server saw.
type capture struct {
	Method     string
	Path       string
	RequestURI string
	Query      string
	Header     http.Header
	Body       string
}

type stub struct {
	*httptest.Server
	last   capture
	status int
	body   string
	header map[string]string
}

func newStub(t *testing.T) *stub {
	t.Helper()
	s := &stub{status: http.StatusOK, body: `{"ok":true}`}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/openapi/") || strings.HasPrefix(r.URL.Path, "/swagger/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		s.last = capture{
			Method:     r.Method,
			Path:       r.URL.Path,
			RequestURI: r.RequestURI,
			Query:      r.URL.RawQuery,
			Header:     r.Header.Clone(),
			Body:       string(body),
		}
		for k, v := range s.header {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(s.body))
	}))
	t.Cleanup(s.Close)
	return s
}

// liveFixture points a config at a stub server.
func liveFixture(t *testing.T, srv *stub, extra string) *fixture {
	t.Helper()
	f := newFixture(t, `
name = "orders"
default_env = "dev"

[env.dev]
base_url = "`+srv.URL+`"
auth = "orders-dev"
`+extra)
	f.writeCredentials(t, bearerCreds)
	return f
}

func (f *fixture) runWith(t *testing.T, stdin string, tty bool, args ...string) result {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rt := &Runtime{
		Globals:    &Globals{},
		Stdout:     stdout,
		Stderr:     stderr,
		Input:      strings.NewReader(stdin),
		StdinIsTTY: tty,
		Dir:        f.dir,
	}
	rt.Globals.prescan(args)
	code := Run(rt, args)
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

const bearerCreds = `
[orders-dev]
type = "bearer"
token = "super-secret-token"
`

func TestRawGet(t *testing.T) {
	srv := newStub(t)
	f := liveFixture(t, srv, "")
	f.writeCredentials(t, bearerCreds)

	got := f.run(t, "raw", "GET", "/healthz")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if got.stdout != "{\"ok\":true}\n" {
		t.Errorf("stdout = %q, want the body", got.stdout)
	}
	if srv.last.Method != "GET" || srv.last.Path != "/healthz" {
		t.Errorf("server saw %s %s, want GET /healthz", srv.last.Method, srv.last.Path)
	}
	if auth := srv.last.Header.Get("Authorization"); auth != "Bearer super-secret-token" {
		t.Errorf("Authorization = %q, want the bearer token applied", auth)
	}
}

func TestRawSendsQueryAndHeaders(t *testing.T) {
	srv := newStub(t)
	f := liveFixture(t, srv, "")
	f.writeCredentials(t, bearerCreds)

	got := f.run(t, "raw", "GET", "/api/orders?status=open",
		"--query", "page=2", "--header", "X-Tenant: acme")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if srv.last.Query != "page=2&status=open" {
		t.Errorf("query = %q, want both sources merged", srv.last.Query)
	}
	if srv.last.Header.Get("X-Tenant") != "acme" {
		t.Errorf("X-Tenant = %q, want acme", srv.last.Header.Get("X-Tenant"))
	}
}

func TestRawBodySources(t *testing.T) {
	file := filepath.Join(t.TempDir(), "order.json")
	if err := os.WriteFile(file, []byte(`{"from":"file"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{"inline", []string{"--data", `{"from":"inline"}`}, "", `{"from":"inline"}`},
		{"file", []string{"--data", "@" + file}, "", `{"from":"file"}`},
		{"stdin", []string{"--data", "@-"}, `{"from":"stdin"}`, `{"from":"stdin"}`},
		{"fields", []string{"--field", "sku=A1", "--field", "qty:=3"}, "", `{"qty":3,"sku":"A1"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newStub(t)
			f := liveFixture(t, srv, "")
			f.writeCredentials(t, bearerCreds)

			args := append([]string{"raw", "POST", "/api/orders", "--yes"}, tt.args...)
			got := f.runWith(t, tt.stdin, false, args...)

			if got.code != output.ExitOK {
				t.Fatalf("exit = %d (%s)", got.code, got.stderr)
			}
			if srv.last.Body != tt.want {
				t.Errorf("body = %q, want %q", srv.last.Body, tt.want)
			}
			if ct := srv.last.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
		})
	}
}

func TestRawRejectsDataAndFieldTogether(t *testing.T) {
	srv := newStub(t)
	f := liveFixture(t, srv, "")

	got := f.run(t, "raw", "POST", "/x", "--yes", "--data", "{}", "--field", "a=1")

	if got.code != output.ExitUsage {
		t.Errorf("exit = %d, want %d", got.code, output.ExitUsage)
	}
}

func TestRawMutationWithoutATTYNeedsYes(t *testing.T) {
	srv := newStub(t)
	f := liveFixture(t, srv, "")
	f.writeCredentials(t, bearerCreds)

	got := f.runWith(t, "", false, "raw", "POST", "/api/orders", "--data", "{}")

	if got.code != output.ExitBlocked {
		t.Errorf("exit = %d, want %d", got.code, output.ExitBlocked)
	}
	if !strings.Contains(got.stderr, "--yes") {
		t.Errorf("stderr = %q, want it to name --yes", got.stderr)
	}
	if srv.last.Method != "" {
		t.Errorf("server saw %s, want nothing sent", srv.last.Method)
	}
}

func TestRawMutationPromptsAtATerminal(t *testing.T) {
	tests := []struct {
		name     string
		answer   string
		wantSent bool
		wantCode int
	}{
		{"accepted", "y\n", true, output.ExitOK},
		{"declined", "n\n", false, output.ExitBlocked},
		{"empty answer declines", "\n", false, output.ExitBlocked},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newStub(t)
			f := liveFixture(t, srv, "")
			f.writeCredentials(t, bearerCreds)

			got := f.runWith(t, tt.answer, true, "raw", "POST", "/api/orders", "--data", "{}")

			if got.code != tt.wantCode {
				t.Errorf("exit = %d, want %d (%s)", got.code, tt.wantCode, got.stderr)
			}
			if sent := srv.last.Method != ""; sent != tt.wantSent {
				t.Errorf("sent = %v, want %v", sent, tt.wantSent)
			}
			if !strings.Contains(got.stderr, "Proceed?") {
				t.Errorf("stderr = %q, want the prompt on stderr", got.stderr)
			}
			if strings.Contains(got.stdout, "Proceed?") {
				t.Errorf("stdout = %q, want the prompt kept off stdout", got.stdout)
			}
		})
	}
}

func TestRawReadonlyEnvironmentBlocksMutations(t *testing.T) {
	srv := newStub(t)
	f := liveFixture(t, srv, "\n[env.prod]\nbase_url = \""+srv.URL+"\"\nreadonly = true\n")
	f.writeCredentials(t, bearerCreds)

	got := f.runWith(t, "y\n", true, "raw", "POST", "/api/orders", "--env", "prod", "--yes", "--data", "{}")

	if got.code != output.ExitBlocked {
		t.Errorf("exit = %d, want %d", got.code, output.ExitBlocked)
	}
	if !strings.Contains(got.stderr, "readonly") {
		t.Errorf("stderr = %q, want it to name the rule", got.stderr)
	}
	if srv.last.Method != "" {
		t.Errorf("server saw %s, want nothing sent", srv.last.Method)
	}
}

func TestDryRunIsStillBlockedByReadonly(t *testing.T) {
	srv := newStub(t)
	f := liveFixture(t, srv, "\n[env.prod]\nbase_url = \""+srv.URL+"\"\nreadonly = true\n")

	got := f.run(t, "raw", "POST", "/api/orders", "--env", "prod", "--dry-run", "--data", "{}")

	if got.code != output.ExitBlocked {
		t.Errorf("exit = %d, want %d", got.code, output.ExitBlocked)
	}
	if got.stdout != "" {
		t.Errorf("stdout = %q, want nothing printed for a request the env forbids", got.stdout)
	}
}

func TestRawReadonlyStillAllowsReads(t *testing.T) {
	srv := newStub(t)
	f := liveFixture(t, srv, "\n[env.prod]\nbase_url = \""+srv.URL+"\"\nreadonly = true\n")

	got := f.run(t, "raw", "GET", "/healthz", "--env", "prod")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
}

func TestRawDryRunSendsNothingAndRedacts(t *testing.T) {
	srv := newStub(t)
	f := liveFixture(t, srv, "")
	f.writeCredentials(t, bearerCreds)

	got := f.run(t, "raw", "POST", "/api/orders", "--data", `{"sku":"A1"}`, "--dry-run")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if srv.last.Method != "" {
		t.Errorf("server saw %s, want a dry run to send nothing", srv.last.Method)
	}
	if strings.Contains(got.stdout+got.stderr, "super-secret-token") {
		t.Errorf("dry run leaked the token:\n%s%s", got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "Authorization: Bearer <redacted>") {
		t.Errorf("stdout = %q, want a redacted Authorization header", got.stdout)
	}
	for _, want := range []string{"POST " + srv.URL + "/api/orders", "env: dev", `{"sku":"A1"}`} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, got.stdout)
		}
	}
}

func TestDryRunWorksWithoutYesOnAMutation(t *testing.T) {
	srv := newStub(t)
	f := liveFixture(t, srv, "")
	f.writeCredentials(t, bearerCreds)

	got := f.runWith(t, "", false, "raw", "DELETE", "/api/orders/1", "--dry-run")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if !strings.HasPrefix(got.stdout, "DELETE ") {
		t.Errorf("stdout = %q, want the request line", got.stdout)
	}
}

func TestVerboseNeverPrintsASecret(t *testing.T) {
	// --dry-run returns before the verbose block, so both paths need covering:
	// the dry run is the one an agent reads, the live send is the one a human does.
	tests := []struct {
		name string
		args []string
	}{
		{"dry run", []string{"--dry-run", "--verbose", "--include-headers"}},
		{"live request", []string{"--yes", "--verbose", "--include-headers"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newStub(t)
			f := liveFixture(t, srv, "")
			f.writeCredentials(t, `
[orders-dev]
type = "basic"
username = "svc"
password = "hunter2-secret"
`)

			args := append([]string{"raw", "POST", "/api/orders?token=hunter2-secret",
				"--data", `{"password":"hunter2-secret"}`}, tt.args...)
			got := f.runWith(t, "", false, args...)

			combined := got.stdout + got.stderr
			if strings.Contains(combined, "hunter2-secret") {
				t.Errorf("output leaked the password:\n%s", combined)
			}
			if strings.Contains(combined, "c3ZjOmh1bnRlcjItc2VjcmV0") {
				t.Errorf("output leaked the encoded basic credential:\n%s", combined)
			}
			// Without a positive assertion this test passes when nothing is printed.
			// A dry run renders the headers on stdout, a live send on stderr.
			if !strings.Contains(combined, "Authorization") {
				t.Errorf("output = %q, want the request headers to have been rendered", combined)
			}
			if !strings.Contains(combined, "<redacted>") {
				t.Errorf("output = %q, want the redaction visible", combined)
			}
		})
	}
}

func TestSafetyRulesCoverEveryMutatingVerb(t *testing.T) {
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "PURGE"} {
		t.Run(method, func(t *testing.T) {
			srv := newStub(t)
			f := liveFixture(t, srv, "")

			got := f.runWith(t, "", false, "raw", method, "/api/orders/1", "--data", "{}")

			if got.code != output.ExitBlocked {
				t.Errorf("exit = %d, want %d (%s)", got.code, output.ExitBlocked, got.stderr)
			}
			if srv.last.Method != "" {
				t.Errorf("server saw %s, want nothing sent", srv.last.Method)
			}
		})
	}
}

func TestReadonlyBlocksEveryMutatingVerb(t *testing.T) {
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			srv := newStub(t)
			f := liveFixture(t, srv, "\n[env.prod]\nbase_url = \""+srv.URL+"\"\nreadonly = true\n")

			got := f.runWith(t, "", false, "raw", method, "/api/orders/1", "--env", "prod", "--yes", "--data", "{}")

			if got.code != output.ExitBlocked {
				t.Errorf("exit = %d, want %d", got.code, output.ExitBlocked)
			}
			if srv.last.Method != "" {
				t.Errorf("server saw %s, want nothing sent", srv.last.Method)
			}
		})
	}
}

func TestRawExitCodesFollowTheStatus(t *testing.T) {
	tests := []struct {
		status int
		want   int
	}{
		{200, output.ExitOK},
		{201, output.ExitOK},
		{400, output.ExitClient},
		{401, output.ExitAuth},
		{403, output.ExitAuth},
		{404, output.ExitClient},
		{500, output.ExitServer},
		{503, output.ExitServer},
	}

	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			srv := newStub(t)
			srv.status = tt.status
			srv.body = `{"error":"detail"}`
			f := liveFixture(t, srv, "")

			got := f.run(t, "raw", "GET", "/x")

			if got.code != tt.want {
				t.Errorf("exit = %d, want %d", got.code, tt.want)
			}
			if !strings.Contains(got.stdout, `{"error":"detail"}`) {
				t.Errorf("stdout = %q, want the error body kept", got.stdout)
			}
			if tt.want != output.ExitOK && !strings.Contains(got.stderr, "->") {
				t.Errorf("stderr = %q, want a one-line summary", got.stderr)
			}
		})
	}
}

func TestRawTransportFailureIsExitSix(t *testing.T) {
	srv := newStub(t)
	f := liveFixture(t, srv, "")
	srv.Close()

	got := f.run(t, "raw", "GET", "/x")

	if got.code != output.ExitTransport {
		t.Errorf("exit = %d, want %d (%s)", got.code, output.ExitTransport, got.stderr)
	}
}

func TestRawOutputModes(t *testing.T) {
	tests := []struct {
		mode string
		want string
	}{
		{output.ModeJSON, "{\"ok\":true}\n"},
		{output.ModeRaw, `{"ok":true}`},
		{output.ModeStatus, "200\n"},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			srv := newStub(t)
			f := liveFixture(t, srv, "")

			got := f.run(t, "raw", "GET", "/x", "--output", tt.mode)

			if got.stdout != tt.want {
				t.Errorf("stdout = %q, want %q", got.stdout, tt.want)
			}
		})
	}
}

func TestRawUnknownOutputModeIsAUsageError(t *testing.T) {
	srv := newStub(t)
	f := liveFixture(t, srv, "")

	got := f.run(t, "raw", "GET", "/x", "--output", "yaml")

	if got.code != output.ExitUsage {
		t.Errorf("exit = %d, want %d", got.code, output.ExitUsage)
	}
}

func TestRawIncludeHeadersKeepsStdoutClean(t *testing.T) {
	srv := newStub(t)
	srv.header = map[string]string{"X-Trace": "t-1"}
	f := liveFixture(t, srv, "")

	got := f.run(t, "raw", "GET", "/x", "--include-headers")

	if got.stdout != "{\"ok\":true}\n" {
		t.Errorf("stdout = %q, want only the body", got.stdout)
	}
	if !strings.Contains(got.stderr, "X-Trace: t-1") {
		t.Errorf("stderr = %q, want the response headers", got.stderr)
	}
}

func TestRawPathMustStartWithASlash(t *testing.T) {
	srv := newStub(t)
	f := liveFixture(t, srv, "")

	got := f.run(t, "raw", "GET", "healthz")

	if got.code != output.ExitUsage {
		t.Errorf("exit = %d, want %d", got.code, output.ExitUsage)
	}
}

func TestRawNeedsBothArguments(t *testing.T) {
	srv := newStub(t)
	f := liveFixture(t, srv, "")

	got := f.run(t, "raw", "GET")

	if got.code != output.ExitUsage {
		t.Errorf("exit = %d, want %d", got.code, output.ExitUsage)
	}
}
