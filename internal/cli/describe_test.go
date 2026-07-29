package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dotnetemmanuel/blip/internal/output"
)

func TestDescribeCompactIsStableAndComplete(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	first := f.run(t, "describe", "--compact")
	if first.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", first.code, first.stderr)
	}

	second := f.run(t, "describe", "--compact")
	if first.stdout != second.stdout {
		t.Errorf("describe --compact drifted between runs:\n%s\n%s", first.stdout, second.stdout)
	}

	// Every operation named, and nothing else: a count alone would let one
	// disappear and another take its place.
	want := []string{
		"customers orders-get-by-customer-id", "healthz get",
		"orders list", "orders create", "orders get", "orders delete-by-id",
		"orders cancel", "orders lines-get-by-id-by-line-id",
	}
	lines := strings.Split(strings.TrimSpace(first.stdout), "\n")
	if len(lines) != len(want) {
		t.Fatalf("lines = %d, want %d:\n%s", len(lines), len(want), first.stdout)
	}
	for i, name := range want {
		if !strings.HasPrefix(lines[i], name+" ") {
			t.Errorf("line %d = %q, want it to start with %q", i, lines[i], name)
		}
	}
}

func TestDescribeCompactKeepsStdoutPure(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.run(t, "describe", "--compact")

	for _, line := range strings.Split(strings.TrimSpace(got.stdout), "\n") {
		if strings.TrimSpace(line) == "" {
			t.Error("compact output has a blank line")
		}
	}
	if !strings.Contains(got.stderr, "no operationId") {
		t.Errorf("stderr = %q, want describe to always report spec problems", got.stderr)
	}
}

func TestDescribeJSON(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.run(t, "describe", "--json")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}

	var listing struct {
		Title      string `json:"title"`
		Operations []struct {
			Command  string `json:"command"`
			FullName string `json:"full_name"`
			Method   string `json:"method"`
			Path     string `json:"path"`
			Params   []struct {
				Name string `json:"name"`
				In   string `json:"in"`
			} `json:"params"`
			Body *struct {
				Schema string `json:"schema"`
				Flat   bool   `json:"flat"`
			} `json:"body"`
		} `json:"operations"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &listing); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, got.stdout)
	}
	if listing.Title != "Orders API" {
		t.Errorf("title = %q", listing.Title)
	}
	if len(listing.Operations) != 8 {
		t.Errorf("operations = %d, want 8", len(listing.Operations))
	}

	for _, op := range listing.Operations {
		if op.Command == "orders create" {
			if op.Body == nil || op.Body.Schema != "CreateOrderRequest" || !op.Body.Flat {
				t.Errorf("create body = %+v", op.Body)
			}
			return
		}
	}
	t.Error("orders create is missing from the JSON listing")
}

func TestDescribeReadable(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, "")

	got := f.run(t, "describe")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	for _, want := range []string{"Orders API", "List orders", "?query", "orders", "customers"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("describe output is missing %q:\n%s", want, got.stdout)
		}
	}
}

const routesOnlyConfig = `
name = "legacy"
default_env = "dev"

[env.dev]
base_url = "%s"

[[route]]
name = "health"
method = "GET"
path = "/healthz"

[[route]]
name = "reindex"
method = "POST"
path = "/admin/reindex/{tenant}"
summary = "Rebuild the search index for one tenant"
`

// speclessFixture points a route-only config at a server with no spec at all.
func speclessFixture(t *testing.T, url string) *fixture {
	t.Helper()
	return newFixture(t, strings.Replace(routesOnlyConfig, "%s", url, 1))
}

func TestRoutesWorkWithNoSpec(t *testing.T) {
	srv := newStub(t)
	f := speclessFixture(t, srv.URL)

	got := f.run(t, "health")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if srv.last.Method != "GET" || srv.last.Path != "/healthz" {
		t.Errorf("server saw %s %s, want GET /healthz", srv.last.Method, srv.last.Path)
	}
}

func TestRoutePathParameter(t *testing.T) {
	srv := newStub(t)
	f := speclessFixture(t, srv.URL)

	got := f.runWith(t, "", false, "reindex", "acme", "--yes")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if srv.last.Path != "/admin/reindex/acme" {
		t.Errorf("path = %q, want /admin/reindex/acme", srv.last.Path)
	}
}

func TestRouteTakesOpenFlags(t *testing.T) {
	srv := newStub(t)
	f := speclessFixture(t, srv.URL)

	got := f.runWith(t, "", false, "reindex", "acme", "--yes",
		"--query", "full=true", "--header", "X-Tenant: acme", "--data", `{"force":true}`)

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if srv.last.Query != "full=true" {
		t.Errorf("query = %q", srv.last.Query)
	}
	if srv.last.Header.Get("X-Tenant") != "acme" {
		t.Errorf("X-Tenant = %q", srv.last.Header.Get("X-Tenant"))
	}
	if srv.last.Body != `{"force":true}` {
		t.Errorf("body = %q", srv.last.Body)
	}
}

func TestRoutesObeyTheSafetyRules(t *testing.T) {
	srv := newStub(t)
	f := speclessFixture(t, srv.URL)

	got := f.runWith(t, "", false, "reindex", "acme")

	if got.code != output.ExitBlocked {
		t.Errorf("exit = %d, want %d (%s)", got.code, output.ExitBlocked, got.stderr)
	}
	if srv.last.Method != "" {
		t.Errorf("server saw %s, want nothing sent", srv.last.Method)
	}
}

func TestDescribeWithRoutesOnly(t *testing.T) {
	srv := newStub(t)
	f := speclessFixture(t, srv.URL)

	got := f.run(t, "describe", "--compact")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	lines := strings.Split(strings.TrimSpace(got.stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %v, want the two declared routes", lines)
	}
	if !strings.Contains(got.stdout, "(route)") {
		t.Errorf("stdout = %q, want declared routes marked", got.stdout)
	}
}

func TestRoutesMergeIntoASpecTree(t *testing.T) {
	srv := newAPIStub(t, "dotnet9-minimal.json")
	f := apiFixture(t, srv, `
[[route]]
name = "reindex"
method = "POST"
path = "/admin/reindex/{tenant}"
`)

	got := f.run(t, "describe", "--compact")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "/admin/reindex/{tenant}") {
		t.Errorf("stdout is missing the declared route:\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, "/api/orders") {
		t.Errorf("stdout is missing the spec operations:\n%s", got.stdout)
	}
}

func TestRouteCollidingWithABlipCommandIsAConfigError(t *testing.T) {
	srv := newStub(t)
	f := newFixture(t, `
name = "legacy"
[env.dev]
base_url = "`+srv.URL+`"

[[route]]
name = "spec"
method = "GET"
path = "/spec"
`)

	got := f.run(t, "describe", "--compact")

	if got.code != output.ExitConfig {
		t.Errorf("exit = %d, want %d (%s)", got.code, output.ExitConfig, got.stderr)
	}
	if !strings.Contains(got.stderr, "collides") {
		t.Errorf("stderr = %q, want it to explain the collision", got.stderr)
	}
}
