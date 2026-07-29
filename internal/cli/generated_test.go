package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotnetemmanuel/blip/internal/output"
)

// apiStub serves a fixture spec and echoes back whatever it was called with.
type apiStub struct {
	*httptest.Server
	last     capture
	status   int
	response string
}

func newAPIStub(t *testing.T, specFile string) *apiStub {
	t.Helper()
	spec, err := os.ReadFile(filepath.Join("..", "..", "testdata", specFile))
	if err != nil {
		t.Fatal(err)
	}

	s := &apiStub{status: http.StatusOK, response: `{"ok":true}`}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/openapi/v1.json" {
			w.Header().Set("ETag", `"v1"`)
			if r.Header.Get("If-None-Match") == `"v1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(spec)
			return
		}
		body := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			_, _ = r.Body.Read(body)
		}
		s.last = capture{
			Method:     r.Method,
			Path:       r.URL.Path,
			RequestURI: r.RequestURI,
			Query:      r.URL.RawQuery,
			Header:     r.Header.Clone(),
			Body:       string(body),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(s.response))
	}))
	t.Cleanup(s.Close)
	return s
}

func apiFixture(t *testing.T, srv *apiStub, extra string) *fixture {
	t.Helper()
	f := newFixture(t, `
name = "orders"
default_env = "dev"

[env.dev]
base_url = "`+srv.URL+`"
`+extra)
	return f
}

func TestGeneratedGetWithPathParam(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.run(t, "orders", "get", "123")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if srv.last.Method != "GET" || srv.last.Path != "/api/orders/123" {
		t.Errorf("server saw %s %s, want GET /api/orders/123", srv.last.Method, srv.last.Path)
	}
}

func TestGeneratedQueryFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"string", []string{"--status", "open"}, "status=open"},
		{"integer", []string{"--page", "3"}, "page=3"},
		{"repeatable array", []string{"--tag", "a", "--tag", "b"}, "tag=a&tag=b"},
		{"several at once", []string{"--status", "open", "--pageSize", "5"}, "pageSize=5&status=open"},
		{"nothing sends nothing", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newAPIStub(t, "dotnet9-minimal.json")
			f := apiFixture(t, srv, "")

			got := f.run(t, append([]string{"orders", "list"}, tt.args...)...)

			if got.code != output.ExitOK {
				t.Fatalf("exit = %d (%s)", got.code, got.stderr)
			}
			if srv.last.Query != tt.want {
				t.Errorf("query = %q, want %q", srv.last.Query, tt.want)
			}
		})
	}
}

func TestGeneratedDefaultsAreNotSentUnasked(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.run(t, "orders", "list")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if strings.Contains(srv.last.Query, "page=") {
		t.Errorf("query = %q, want the spec default left out", srv.last.Query)
	}
}

func TestGeneratedEnumIsChecked(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.run(t, "orders", "list", "--status", "nonsense")

	if got.code != output.ExitUsage {
		t.Errorf("exit = %d, want %d", got.code, output.ExitUsage)
	}
	if !strings.Contains(got.stderr, "open, shipped, cancelled") {
		t.Errorf("stderr = %q, want the allowed values", got.stderr)
	}
}

func TestGeneratedHeaderFlag(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.run(t, "orders", "get", "7", "--header-x-tenant", "acme")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if srv.last.Header.Get("X-Tenant") != "acme" {
		t.Errorf("X-Tenant = %q, want acme", srv.last.Header.Get("X-Tenant"))
	}
}

func TestGeneratedMissingPathArgIsExitTwo(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.run(t, "orders", "get")

	if got.code != output.ExitUsage {
		t.Errorf("exit = %d, want %d", got.code, output.ExitUsage)
	}
	if !strings.Contains(got.stderr, "<id>") {
		t.Errorf("stderr = %q, want it to name the missing argument", got.stderr)
	}
}

func TestGeneratedTwoPathArgsInPathOrder(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.run(t, "orders", "lines-get-by-id-by-line-id", "abc", "9")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if srv.last.Path != "/api/orders/abc/lines/9" {
		t.Errorf("path = %q, want /api/orders/abc/lines/9", srv.last.Path)
	}
}

func TestGeneratedBodyFromFields(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.runWith(t, "", false, "orders", "create", "--field", "sku=A1", "--field", "quantity:=2", "--yes")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if srv.last.Body != `{"quantity":2,"sku":"A1"}` {
		t.Errorf("body = %q", srv.last.Body)
	}
	if ct := srv.last.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestGeneratedRequiredBodyIsEnforced(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.runWith(t, "", false, "orders", "create", "--yes")

	if got.code != output.ExitUsage {
		t.Errorf("exit = %d, want %d", got.code, output.ExitUsage)
	}
	if !strings.Contains(got.stderr, "--data") {
		t.Errorf("stderr = %q, want it to say how to supply a body", got.stderr)
	}
}

func TestGeneratedMutationsObeyTheSafetyRules(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.runWith(t, "", false, "orders", "create", "--field", "sku=A1")

	if got.code != output.ExitBlocked {
		t.Errorf("exit = %d, want %d (%s)", got.code, output.ExitBlocked, got.stderr)
	}
	if srv.last.Method != "" {
		t.Errorf("server saw %s, want nothing sent", srv.last.Method)
	}
}

func TestGeneratedDryRun(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.run(t, "orders", "cancel", "42", "--field", "reason=changed mind", "--dry-run")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "POST "+srv.URL+"/api/orders/42/cancel") {
		t.Errorf("stdout = %q, want the resolved request line", got.stdout)
	}
	if srv.last.Method != "" {
		t.Errorf("server saw %s, want a dry run to send nothing", srv.last.Method)
	}
}

func TestCallUsesTheStableName(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantPath string
	}{
		{"operationId", []string{"call", "listOrders", "--status", "open"}, "/api/orders"},
		{"derived full name", []string{"call", "orders-delete-by-id", "5", "--yes"}, "/api/orders/5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newAPIStub(t, "dotnet9-minimal.json")
			f := apiFixture(t, srv, "")

			got := f.runWith(t, "", false, tt.args...)

			if got.code != output.ExitOK {
				t.Fatalf("exit = %d (%s)", got.code, got.stderr)
			}
			if srv.last.Path != tt.wantPath {
				t.Errorf("path = %q, want %q", srv.last.Path, tt.wantPath)
			}
		})
	}
}

func TestCallUnknownOperationIsExitTwo(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.run(t, "call", "noSuchOperation")

	if got.code != output.ExitUsage {
		t.Errorf("exit = %d, want %d", got.code, output.ExitUsage)
	}
}

func TestGeneratedHelpCarriesTheSpecDocumentation(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.run(t, "orders", "list", "--help")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	for _, want := range []string{
		"List orders",
		"newest first",
		"operationId: listOrders",
		"Filter by order status",
		"1-based page number",
	} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("help is missing %q:\n%s", want, got.stdout)
		}
	}
}

func TestDerivedNameHelpSaysSo(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.run(t, "orders", "delete-by-id", "--help")

	if !strings.Contains(got.stdout, "derived") {
		t.Errorf("help does not explain the derived name:\n%s", got.stdout)
	}
}

func TestDerivedNameWarningAppearsWhenTheSpecIsFetched(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	first := f.run(t, "orders", "get", "1")
	if !strings.Contains(first.stderr, "no operationId") {
		t.Errorf("stderr = %q, want the derived-name warning on a fresh spec", first.stderr)
	}

	second := f.run(t, "orders", "get", "1")
	if strings.Contains(second.stderr, "no operationId") {
		t.Errorf("stderr = %q, want the warning suppressed once the spec is unchanged", second.stderr)
	}
}

func TestResponseValidationWarnsByDefault(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	srv.response = `{"id":"not-a-uuid","total":"not-a-number"}`
	f := apiFixture(t, srv, "")

	got := f.run(t, "orders", "get", "1")

	if got.code != output.ExitOK {
		t.Errorf("exit = %d, want drift to be a warning by default", got.code)
	}
	if !strings.Contains(got.stderr, "does not match the spec") {
		t.Errorf("stderr = %q, want a drift warning", got.stderr)
	}
	if !strings.Contains(got.stdout, "not-a-uuid") {
		t.Errorf("stdout = %q, want the body regardless", got.stdout)
	}
}

func TestResponseValidationFailsUnderStrict(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	srv.response = `{"total":"not-a-number"}`
	f := apiFixture(t, srv, "")

	got := f.run(t, "orders", "get", "1", "--strict")

	if got.code != output.ExitSchema {
		t.Errorf("exit = %d, want %d", got.code, output.ExitSchema)
	}
}

func TestResponseValidationPassesOnAGoodBody(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	srv.response = `{"id":"7f000101-0000-4000-8000-000000000000","status":"open","total":12.5}`
	f := apiFixture(t, srv, "")

	got := f.run(t, "orders", "get", "1", "--strict")

	if got.code != output.ExitOK {
		t.Errorf("exit = %d, want %d (%s)", got.code, output.ExitOK, got.stderr)
	}
}

func TestGeneratedTreeIsSkippedForSpeclessCommands(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")
	srv.Close()

	// The spec is unreachable, so anything that reached for it would fail here.
	for _, args := range [][]string{{"version"}, {"envs"}} {
		got := f.run(t, args...)
		if got.code != output.ExitOK {
			t.Errorf("%v: exit = %d, want it to work without a spec (%s)", args, got.code, got.stderr)
		}
	}
}

func TestSwashbuckleSpecProducesWorkingCommands(t *testing.T) {
	srv := newAPIStub(t, "swashbuckle.json")
	f := apiFixture(t, srv, "")

	got := f.run(t, "products", "get-by-id", "42")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if srv.last.Path != "/api/Products/42" {
		t.Errorf("path = %q, want /api/Products/42", srv.last.Path)
	}
}

func TestRequiredQueryParamIsEnforced(t *testing.T) {
	srv := newAPIStub(t, "swashbuckle.json")
	f := apiFixture(t, srv, "")

	got := f.runWith(t, "", false, "inventory", "adjust", "SKU1", "--yes")

	if got.code != output.ExitUsage {
		t.Errorf("exit = %d, want %d (%s)", got.code, output.ExitUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "--delta") {
		t.Errorf("stderr = %q, want it to name the missing parameter", got.stderr)
	}
}

func TestGeneratedOperationsAreListedInHelp(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.run(t, "--help")

	for _, want := range []string{"orders", "customers", "healthz", "call"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("root help is missing %q:\n%s", want, got.stdout)
		}
	}
}

func TestJSONOutputStaysParseable(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	srv.response = `{"id":"7f000101-0000-4000-8000-000000000000"}`
	f := apiFixture(t, srv, "")

	got := f.run(t, "orders", "get", "1")

	var parsed map[string]any
	if err := json.Unmarshal([]byte(got.stdout), &parsed); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, got.stdout)
	}
}
