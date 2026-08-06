package request

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/dotnetemmanuel/blip/internal/build"
)

func op(method, path string, params ...build.Param) *build.Operation {
	return &build.Operation{Group: "orders", Name: "get", Method: method, Path: path, Params: params}
}

func TestForOperationSubstitutesPathParamsInOrder(t *testing.T) {
	o := op(http.MethodGet, "/orders/{orderId}/lines/{lineId}",
		build.Param{Name: "orderId", In: build.InPath, Required: true},
		build.Param{Name: "lineId", In: build.InPath, Required: true},
	)

	req, err := ForOperation(o, OperationValues{Path: []string{"7", "3"}})
	if err != nil {
		t.Fatalf("ForOperation returned %v", err)
	}
	if want := "/orders/7/lines/3"; req.Path != want {
		t.Errorf("path = %q, want %q", req.Path, want)
	}
}

func TestForOperationEscapesAPathValue(t *testing.T) {
	o := op(http.MethodGet, "/orders/{id}", build.Param{Name: "id", In: build.InPath, Required: true})

	req, err := ForOperation(o, OperationValues{Path: []string{"a/b c"}})
	if err != nil {
		t.Fatalf("ForOperation returned %v", err)
	}
	if want := "/orders/a%2Fb%20c"; req.Path != want {
		t.Errorf("path = %q, want %q", req.Path, want)
	}
}

// A climbing value must not reach a server, whichever front end supplied it.
func TestForOperationPathValueCannotClimb(t *testing.T) {
	o := op(http.MethodGet, "/orders/{id}", build.Param{Name: "id", In: build.InPath, Required: true})

	req, err := ForOperation(o, OperationValues{Path: []string{".."}})
	if err != nil {
		t.Fatalf("ForOperation returned %v", err)
	}
	base, _ := url.Parse("https://example.test")
	if _, err := req.HTTPRequest(context.Background(), base); err == nil {
		t.Fatal("a .. path value was accepted, want it refused")
	} else if !strings.Contains(err.Error(), "..") {
		t.Errorf("error = %q, want it to name the .. segment", err)
	}
}

func TestForOperationRefusesAPathValueCountMismatch(t *testing.T) {
	o := op(http.MethodGet, "/orders/{id}", build.Param{Name: "id", In: build.InPath, Required: true})

	if _, err := ForOperation(o, OperationValues{}); err == nil {
		t.Fatal("no path values were accepted for an operation that needs one, want an error")
	}
}

func TestForOperationCarriesQueryHeaderAndBody(t *testing.T) {
	o := op(http.MethodPost, "/orders")
	o.Body = &build.Body{ContentType: "application/json", Flat: true}

	req, err := ForOperation(o, OperationValues{
		Query:  url.Values{"page": {"2"}, "tag": {"a", "b"}},
		Header: http.Header{"X-Trace": {"abc"}},
		Body:   []byte(`{"name":"x"}`),
	})
	if err != nil {
		t.Fatalf("ForOperation returned %v", err)
	}
	if got := req.Query["tag"]; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("query tag = %v, want both values in order", got)
	}
	if got := req.Header.Get("X-Trace"); got != "abc" {
		t.Errorf("header X-Trace = %q, want abc", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want the body content type", got)
	}
}

func TestForOperationSetsNoContentTypeWithoutABody(t *testing.T) {
	o := op(http.MethodPost, "/orders")
	o.Body = &build.Body{ContentType: "application/json"}

	req, err := ForOperation(o, OperationValues{})
	if err != nil {
		t.Fatalf("ForOperation returned %v", err)
	}
	if got := req.Header.Get("Content-Type"); got != "" {
		t.Errorf("Content-Type = %q, want none when there is no body", got)
	}
}

func TestMissingFromReportsRequiredParamsInRequestOrder(t *testing.T) {
	o := op(http.MethodGet, "/orders/{id}",
		build.Param{Name: "id", In: build.InPath, Required: true},
		build.Param{Name: "page", In: build.InQuery, Required: true},
		build.Param{Name: "size", In: build.InQuery},
		build.Param{Name: "X-Tenant", In: build.InHeader, Required: true},
	)

	missing := MissingFrom(o, OperationValues{Path: []string{""}})

	var names []string
	for _, p := range missing.Params {
		names = append(names, p.Name)
	}
	want := []string{"id", "page", "X-Tenant"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("missing = %v, want %v", names, want)
	}
}

func TestMissingFromIgnoresASuppliedOptional(t *testing.T) {
	o := op(http.MethodGet, "/orders", build.Param{Name: "size", In: build.InQuery})

	if missing := MissingFrom(o, OperationValues{}); missing.Any() {
		t.Errorf("missing = %+v, want nothing for an absent optional", missing)
	}
}

func TestMissingFromReportsARequiredBody(t *testing.T) {
	o := op(http.MethodPost, "/orders")
	o.Body = &build.Body{ContentType: "application/json", Required: true}

	if missing := MissingFrom(o, OperationValues{}); !missing.Body {
		t.Error("an absent required body was not reported")
	}
	if missing := MissingFrom(o, OperationValues{Body: []byte("{}")}); missing.Body {
		t.Error("a supplied required body was reported missing")
	}
}

// A route has no spec behind it, so nothing about it is required and whatever
// the caller passed goes through untouched.
func TestForOperationPassesARouteThrough(t *testing.T) {
	o := &build.Operation{Method: http.MethodPost, Path: "/anything", Source: build.SourceRoute}

	req, err := ForOperation(o, OperationValues{
		Query: url.Values{"q": {"1"}},
		Body:  []byte("hello"),
	})
	if err != nil {
		t.Fatalf("ForOperation returned %v", err)
	}
	if req.Query.Get("q") != "1" || string(req.Body) != "hello" {
		t.Errorf("route request = %+v, want the caller's query and body", req)
	}
	if missing := MissingFrom(o, OperationValues{}); missing.Any() {
		t.Errorf("missing = %+v, want nothing required of a route", missing)
	}
}
