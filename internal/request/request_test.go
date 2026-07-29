package request

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotnetemmanuel/blip/internal/output"
)

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestResolveURL(t *testing.T) {
	tests := []struct {
		name  string
		base  string
		path  string
		query url.Values
		want  string
	}{
		{"plain", "https://api.test", "/orders", nil, "https://api.test/orders"},
		{"base with a path prefix", "https://api.test/api", "/orders", nil, "https://api.test/api/orders"},
		{"trailing slash on the base", "https://api.test/api/", "/orders", nil, "https://api.test/api/orders"},
		{"query in the path", "https://api.test", "/orders?status=open", nil, "https://api.test/orders?status=open"},
		{"query from flags", "https://api.test", "/orders", url.Values{"page": {"2"}}, "https://api.test/orders?page=2"},
		{"query from both", "https://api.test", "/orders?status=open", url.Values{"page": {"2"}}, "https://api.test/orders?page=2&status=open"},
		{"query already on the base", "https://api.test?tenant=acme", "/orders", nil, "https://api.test/orders?tenant=acme"},
		{"repeated values", "https://api.test", "/orders", url.Values{"id": {"1", "2"}}, "https://api.test/orders?id=1&id=2"},
		{"space in a path segment", "https://api.test", "/orders/a b", nil, "https://api.test/orders/a%20b"},
		{"port survives", "https://localhost:7284", "/healthz", nil, "https://localhost:7284/healthz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveURL(mustParse(t, tt.base), tt.path, tt.query)
			if err != nil {
				t.Fatalf("ResolveURL: %v", err)
			}
			if got.String() != tt.want {
				t.Errorf("ResolveURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveURLRejectsAPathWithoutASlash(t *testing.T) {
	_, err := ResolveURL(mustParse(t, "https://api.test"), "orders", nil)

	if err == nil {
		t.Fatal("ResolveURL succeeded, want a usage error")
	}
	if got := output.ExitCodeFor(err); got != output.ExitUsage {
		t.Errorf("exit code = %d, want %d", got, output.ExitUsage)
	}
}

func TestResolveURLCannotEscapeTheBaseHost(t *testing.T) {
	got, err := ResolveURL(mustParse(t, "https://api.test"), "//evil.test/x", nil)
	if err != nil {
		t.Fatalf("ResolveURL: %v", err)
	}
	if got.Host != "api.test" {
		t.Errorf("host = %q, want the base host to win", got.Host)
	}
}

func TestHTTPRequestDefaults(t *testing.T) {
	r := &Request{Method: "post", Path: "/orders", Body: []byte(`{"a":1}`)}

	req, err := r.HTTPRequest(context.Background(), mustParse(t, "https://api.test"))
	if err != nil {
		t.Fatalf("HTTPRequest: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", req.Method)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want application/json", got)
	}
}

func TestHTTPRequestKeepsExplicitHeaders(t *testing.T) {
	r := &Request{
		Method: "POST",
		Path:   "/x",
		Body:   []byte("a=1"),
		Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "Accept": {"text/plain"}},
	}

	req, err := r.HTTPRequest(context.Background(), mustParse(t, "https://api.test"))
	if err != nil {
		t.Fatal(err)
	}

	if got := req.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want the explicit value", got)
	}
	if got := req.Header.Get("Accept"); got != "text/plain" {
		t.Errorf("Accept = %q, want the explicit value", got)
	}
}

func TestHTTPRequestRejectsANonMethod(t *testing.T) {
	for _, method := range []string{"", "GET /x", "not a method"} {
		r := &Request{Method: method, Path: "/x"}
		if _, err := r.HTTPRequest(context.Background(), mustParse(t, "https://api.test")); err == nil {
			t.Errorf("HTTPRequest(%q) succeeded, want a usage error", method)
		}
	}
}

func TestDoReturnsStatusHeadersAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orders" || r.URL.Query().Get("status") != "open" {
			t.Errorf("got %s, want /orders?status=open", r.URL)
		}
		w.Header().Set("X-Trace", "abc")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	r := &Request{Method: "GET", Path: "/orders", Query: url.Values{"status": {"open"}}}
	req, err := r.HTTPRequest(context.Background(), mustParse(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := Do(srv.Client(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.Status != http.StatusCreated {
		t.Errorf("Status = %d, want 201", resp.Status)
	}
	if got := resp.Header.Get("X-Trace"); got != "abc" {
		t.Errorf("X-Trace = %q, want abc", got)
	}
	if string(resp.Body) != `{"id":1}` {
		t.Errorf("Body = %q, want the payload", resp.Body)
	}
}

func TestDoTurnsAConnectionFailureIntoATransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := srv.Client()
	srv.Close()

	r := &Request{Method: "GET", Path: "/x"}
	req, err := r.HTTPRequest(context.Background(), mustParse(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}

	_, err = Do(client, req)
	if err == nil {
		t.Fatal("Do succeeded against a closed server")
	}
	if got := output.ExitCodeFor(err); got != output.ExitTransport {
		t.Errorf("exit code = %d, want %d", got, output.ExitTransport)
	}
}

func TestData(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "body.json")
	if err := os.WriteFile(file, []byte(`{"from":"file"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		value string
		stdin string
		want  string
	}{
		{"inline", `{"a":1}`, "", `{"a":1}`},
		{"file", "@" + file, "", `{"from":"file"}`},
		{"stdin", StdinMarker, `{"from":"stdin"}`, `{"from":"stdin"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Data(tt.value, strings.NewReader(tt.stdin))
			if err != nil {
				t.Fatalf("Data: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Data = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDataMissingFileIsAUsageError(t *testing.T) {
	_, err := Data("@/definitely/not/here.json", nil)

	if err == nil {
		t.Fatal("Data succeeded, want an error")
	}
	if got := output.ExitCodeFor(err); got != output.ExitUsage {
		t.Errorf("exit code = %d, want %d", got, output.ExitUsage)
	}
}

func TestFields(t *testing.T) {
	tests := []struct {
		name  string
		pairs []string
		want  string
	}{
		{"strings", []string{"sku=A1", "note=hello world"}, `{"note":"hello world","sku":"A1"}`},
		{"raw number", []string{"qty:=3"}, `{"qty":3}`},
		{"raw boolean", []string{"active:=true"}, `{"active":true}`},
		{"raw null", []string{"cancelled:=null"}, `{"cancelled":null}`},
		{"raw array", []string{"tags:=[\"a\",\"b\"]"}, `{"tags":["a","b"]}`},
		{"digits stay a string without the colon", []string{"id=123"}, `{"id":"123"}`},
		{"value containing equals", []string{"q=a=b"}, `{"q":"a=b"}`},
		{"empty value", []string{"note="}, `{"note":""}`},
		{"keys are sorted for stability", []string{"b=2", "a=1"}, `{"a":"1","b":"2"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Fields(tt.pairs)
			if err != nil {
				t.Fatalf("Fields: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Fields = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestFieldsRejects(t *testing.T) {
	for _, pairs := range [][]string{{"novalue"}, {"=orphan"}, {"bad:=not json"}, {":=1"}} {
		if _, err := Fields(pairs); err == nil {
			t.Errorf("Fields(%v) succeeded, want a usage error", pairs)
		}
	}
}

func TestParseHeaders(t *testing.T) {
	header, err := ParseHeaders([]string{"X-Trace: abc", "X-Tenant=acme", "Accept:  text/plain "})
	if err != nil {
		t.Fatalf("ParseHeaders: %v", err)
	}

	want := map[string]string{"X-Trace": "abc", "X-Tenant": "acme", "Accept": "text/plain"}
	for name, value := range want {
		if got := header.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestParseHeadersRejectsGarbage(t *testing.T) {
	if _, err := ParseHeaders([]string{"nonsense"}); err == nil {
		t.Error("ParseHeaders succeeded on a value with no separator")
	}
}

func TestParseQuery(t *testing.T) {
	query, err := ParseQuery([]string{"status=open", "id=1", "id=2"})
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}

	if got := query.Encode(); got != "id=1&id=2&status=open" {
		t.Errorf("ParseQuery = %q", got)
	}
}
