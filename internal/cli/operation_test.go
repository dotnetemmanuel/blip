package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dotnetemmanuel/blip/internal/output"
)

// specServing writes a spec to a temp file and serves it, so a test can pin
// exactly the shape it wants to exercise.
func specServing(t *testing.T, spec string) *apiStub {
	t.Helper()
	s := &apiStub{status: http.StatusOK, response: `{"ok":true}`}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/openapi/v1.json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(spec))
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

func specWithQueryParam(name, typ string) string {
	return `{"openapi":"3.0.1","info":{"title":"T","version":"1"},"paths":{
		"/api/things":{
		 "get":{"tags":["Things"],"operationId":"listThings",
		  "parameters":[{"name":"` + name + `","in":"query","schema":{"type":"` + typ + `"}}],
		  "responses":{"200":{"description":"OK"}}},
		 "post":{"tags":["Things"],"operationId":"createThing",
		  "parameters":[{"name":"` + name + `","in":"query","schema":{"type":"` + typ + `"}}],
		  "responses":{"200":{"description":"OK"}}}}}}`
}

// A spec must never be able to take over a flag blip's own behaviour depends on.
// Letting it own --dry-run would mean a mutation could be sent by the very
// command an agent uses to check itself.
func TestSpecParameterCannotTakeOverAGlobalFlag(t *testing.T) {
	srv := specServing(t, specWithQueryParam("dry-run", "boolean"))
	f := apiFixture(t, srv, "")

	got := f.runWith(t, "", false, "things", "create", "--yes", "--dry-run")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if srv.last.Method != "" {
		t.Errorf("server saw %s, want --dry-run to still mean dry run", srv.last.Method)
	}
	if !strings.HasPrefix(got.stdout, "POST ") {
		t.Errorf("stdout = %q, want the dry-run rendering", got.stdout)
	}
}

func TestSpecParameterCannotTakeOverTheOutputMode(t *testing.T) {
	srv := specServing(t, specWithQueryParam("output", "string"))
	f := apiFixture(t, srv, "")

	got := f.run(t, "things", "list", "--output", "status")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if strings.TrimSpace(got.stdout) != "200" {
		t.Errorf("stdout = %q, want only the status code", got.stdout)
	}
	if srv.last.Query != "" {
		t.Errorf("query = %q, want --output not to have become a query parameter", srv.last.Query)
	}

	bad := f.run(t, "things", "list", "--output", "bogus")
	if bad.code != output.ExitUsage {
		t.Errorf("exit = %d, want an unknown output mode to still be a usage error", bad.code)
	}
}

func TestShadowedParameterIsRenamedAndStillSends(t *testing.T) {
	srv := specServing(t, specWithQueryParam("output", "string"))
	f := apiFixture(t, srv, "")

	got := f.run(t, "things", "list", "--query-output", "csv")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	if srv.last.Query != "output=csv" {
		t.Errorf("query = %q, want the renamed flag to send the real parameter name", srv.last.Query)
	}

	help := f.run(t, "things", "list", "--help")
	if !strings.Contains(help.stdout, "--query-output") {
		t.Errorf("help does not mention the renamed flag:\n%s", help.stdout)
	}
	if !strings.Contains(help.stdout, "renamed") {
		t.Errorf("help does not explain the rename:\n%s", help.stdout)
	}
}

// Two headers differing only in case, and a parameter named after a flag blip
// registers for bodies, both used to panic while building the tree.
func TestCollidingParameterNamesDoNotPanic(t *testing.T) {
	spec := `{"openapi":"3.0.1","info":{"title":"T","version":"1"},"paths":{
		"/api/things":{"post":{"tags":["Things"],"operationId":"createThing",
		 "parameters":[
		  {"name":"data","in":"query","schema":{"type":"string"}},
		  {"name":"X-Tenant","in":"header","schema":{"type":"string"}},
		  {"name":"x-tenant","in":"header","schema":{"type":"string"}}],
		 "requestBody":{"required":true,"content":{"application/json":{"schema":{
		  "type":"object","properties":{"a":{"type":"string"}}}}}},
		 "responses":{"200":{"description":"OK"}}}}}}`
	srv := specServing(t, spec)
	f := apiFixture(t, srv, "")

	got := f.run(t, "things", "create", "--help")

	if got.code != output.ExitOK {
		t.Fatalf("exit = %d (%s)", got.code, got.stderr)
	}
	for _, want := range []string{"--data", "--query-data", "--header-x-tenant", "--header-x-tenant-2"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("help is missing %q:\n%s", want, got.stdout)
		}
	}
}

// Booleans and numbers had no fixture, so their whole binding path was dead.
func TestBooleanAndNumberParameterBinding(t *testing.T) {
	spec := `{"openapi":"3.0.1","info":{"title":"T","version":"1"},"paths":{
		"/api/things":{"get":{"tags":["Things"],"operationId":"listThings",
		 "parameters":[
		  {"name":"archived","in":"query","schema":{"type":"boolean"}},
		  {"name":"minPrice","in":"query","schema":{"type":"number"}},
		  {"name":"limit","in":"query","schema":{"type":"integer"}}],
		 "responses":{"200":{"description":"OK"}}}}}}`

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"boolean true", []string{"--archived"}, "archived=true"},
		{"boolean false", []string{"--archived=false"}, "archived=false"},
		{"number", []string{"--minPrice", "12.5"}, "minPrice=12.5"},
		{"integer", []string{"--limit", "7"}, "limit=7"},
		{"unset sends nothing", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := specServing(t, spec)
			f := apiFixture(t, srv, "")

			got := f.run(t, append([]string{"things", "list"}, tt.args...)...)

			if got.code != output.ExitOK {
				t.Fatalf("exit = %d (%s)", got.code, got.stderr)
			}
			if srv.last.Query != tt.want {
				t.Errorf("query = %q, want %q", srv.last.Query, tt.want)
			}
		})
	}
}

func TestNumberParameterRejectsANonNumber(t *testing.T) {
	spec := `{"openapi":"3.0.1","info":{"title":"T","version":"1"},"paths":{
		"/api/things":{"get":{"tags":["Things"],"operationId":"listThings",
		 "parameters":[{"name":"minPrice","in":"query","schema":{"type":"number"}}],
		 "responses":{"200":{"description":"OK"}}}}}}`
	srv := specServing(t, spec)
	f := apiFixture(t, srv, "")

	got := f.run(t, "things", "list", "--minPrice", "cheap")

	if got.code != output.ExitUsage {
		t.Errorf("exit = %d, want %d", got.code, output.ExitUsage)
	}
	if srv.last.Method != "" {
		t.Error("a request was sent despite the bad flag value")
	}
}

// A path argument is escaped so it addresses the operation it names and nothing
// else. Without this an id read from a previous response could redirect the call.
func TestPathArgumentCannotEscapeTheOperationPath(t *testing.T) {
	tests := []struct {
		name    string
		arg     string
		want    string
		refused bool
	}{
		// A climb is refused outright: encoding it and hoping the server agrees
		// is not a guarantee, since servers differ on whether they decode first.
		{name: "traversal", arg: "../../admin/secrets", refused: true},
		{name: "embedded slash", arg: "a/b", want: "/api/things/a%2Fb"},
		{name: "ordinary id", arg: "7f00-0101", want: "/api/things/7f00-0101"},
		{name: "dots inside a segment are fine", arg: "a..b", want: "/api/things/a..b"},
	}

	spec := `{"openapi":"3.0.1","info":{"title":"T","version":"1"},"paths":{
		"/api/things/{id}":{"get":{"tags":["Things"],"operationId":"getThing",
		 "parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],
		 "responses":{"200":{"description":"OK"}}}}}}`

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := specServing(t, spec)
			f := apiFixture(t, srv, "")

			got := f.run(t, "things", "get", tt.arg)

			if tt.refused {
				if got.code != output.ExitUsage {
					t.Errorf("exit = %d, want %d", got.code, output.ExitUsage)
				}
				if srv.last.Method != "" {
					t.Errorf("server saw %s %s, want nothing sent", srv.last.Method, srv.last.RequestURI)
				}
				return
			}
			if got.code != output.ExitOK {
				t.Fatalf("exit = %d (%s)", got.code, got.stderr)
			}
			// RequestURI is the raw line the server received, before any decoding.
			if srv.last.RequestURI != tt.want {
				t.Errorf("server saw %q, want %q", srv.last.RequestURI, tt.want)
			}
		})
	}
}

func TestMissingRequiredHeadersAreReportedInSpecOrder(t *testing.T) {
	spec := `{"openapi":"3.0.1","info":{"title":"T","version":"1"},"paths":{
		"/api/things":{"get":{"tags":["Things"],"operationId":"listThings",
		 "parameters":[
		  {"name":"X-Alpha","in":"header","required":true,"schema":{"type":"string"}},
		  {"name":"X-Bravo","in":"header","required":true,"schema":{"type":"string"}},
		  {"name":"X-Charlie","in":"header","required":true,"schema":{"type":"string"}},
		  {"name":"X-Delta","in":"header","required":true,"schema":{"type":"string"}}],
		 "responses":{"200":{"description":"OK"}}}}}}`

	want := ""
	for i := 0; i < 15; i++ {
		srv := specServing(t, spec)
		f := apiFixture(t, srv, "")

		got := f.run(t, "things", "list")
		if got.code != output.ExitUsage {
			t.Fatalf("exit = %d, want %d", got.code, output.ExitUsage)
		}
		if want == "" {
			want = got.stderr
			continue
		}
		if got.stderr != want {
			t.Fatalf("the message moved between runs:\n%s\n%s", got.stderr, want)
		}
	}
	if !strings.Contains(want, "--header-x-alpha, --header-x-bravo, --header-x-charlie, --header-x-delta") {
		t.Errorf("stderr = %q, want the headers in spec order", want)
	}
}
