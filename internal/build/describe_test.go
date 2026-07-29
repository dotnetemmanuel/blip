package build

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// golden compares against a checked-in file. describe --compact is a contract an
// agent depends on, so it must not drift without someone noticing.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)

	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden file: %v (run: go test ./internal/build -update)", err)
	}
	if got != string(want) {
		t.Errorf("output does not match %s:\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

func TestCompactLinesGolden(t *testing.T) {
	for _, spec := range []string{"dotnet9-minimal.json", "dotnet10-minimal.json", "swashbuckle.json", "no-tags.json"} {
		t.Run(spec, func(t *testing.T) {
			api := load(t, spec)
			got := strings.Join(api.CompactLines(), "\n") + "\n"
			golden(t, strings.TrimSuffix(spec, ".json")+"-compact.txt", got)
		})
	}
}

func TestDescribeLinesGolden(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")
	got := strings.Join(api.DescribeLines(), "\n") + "\n"
	golden(t, "dotnet9-minimal-describe.txt", got)
}

func TestCompactIsStableAcrossRuns(t *testing.T) {
	first := strings.Join(load(t, "dotnet9-minimal.json").CompactLines(), "\n")
	for i := 0; i < 10; i++ {
		if got := strings.Join(load(t, "dotnet9-minimal.json").CompactLines(), "\n"); got != first {
			t.Fatalf("run %d differs", i)
		}
	}
}

func TestCompactListsEveryOperationExactlyOnce(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")
	lines := api.CompactLines()

	if len(lines) != len(api.Operations) {
		t.Fatalf("lines = %d, operations = %d", len(lines), len(api.Operations))
	}
	seen := map[string]int{}
	for _, op := range api.Operations {
		seen[op.Method+" "+op.Path]++
	}
	for key, count := range seen {
		if count != 1 {
			t.Errorf("%s appears %d times", key, count)
		}
	}
	for _, op := range api.Operations {
		found := false
		for _, line := range lines {
			if strings.Contains(line, op.Method) && strings.Contains(line, op.Path) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s %s is missing from the compact listing", op.Method, op.Path)
		}
	}
}

func TestCompactHasNoDecoration(t *testing.T) {
	for _, line := range load(t, "dotnet9-minimal.json").CompactLines() {
		if strings.TrimSpace(line) == "" {
			t.Error("compact output contains a blank line")
		}
		if strings.ContainsAny(line, "\x1b|+") {
			t.Errorf("compact line has decoration: %q", line)
		}
		if strings.HasSuffix(line, " ") {
			t.Errorf("compact line has trailing space: %q", line)
		}
	}
}

func TestCompactNotation(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")
	lines := map[string]string{}
	for i, op := range api.Operations {
		lines[commandPath(op)] = api.CompactLines()[i]
	}

	tests := []struct {
		command string
		want    []string
	}{
		{"orders list", []string{"GET", "/api/orders", "?status", "?page", "?pageSize", "?tag"}},
		{"orders get", []string{"id", "@X-Tenant"}},
		{"orders create", []string{"POST", "body!:CreateOrderRequest"}},
		{"orders cancel", []string{"id", "body!:CancelReason"}},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			line, ok := lines[tt.command]
			if !ok {
				t.Fatalf("no line for %q", tt.command)
			}
			for _, want := range tt.want {
				if !strings.Contains(line, want) {
					t.Errorf("line %q is missing %q", line, want)
				}
			}
		})
	}
}

func TestRequiredQueryParamIsMarked(t *testing.T) {
	api := load(t, "swashbuckle.json")
	for i, op := range api.Operations {
		if op.Path == "/api/Inventory/{sku}/adjust" {
			if !strings.Contains(api.CompactLines()[i], "?delta*") {
				t.Errorf("line = %q, want ?delta*", api.CompactLines()[i])
			}
			return
		}
	}
	t.Fatal("the adjust operation is missing")
}

func TestListingCarriesTheStableName(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")

	raw, err := json.Marshal(api.Listing())
	if err != nil {
		t.Fatal(err)
	}

	var listing struct {
		Operations []struct {
			FullName string `json:"full_name"`
			Command  string `json:"command"`
			Method   string `json:"method"`
			Path     string `json:"path"`
			Derived  bool   `json:"derived"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Operations) != len(api.Operations) {
		t.Fatalf("operations = %d, want %d", len(listing.Operations), len(api.Operations))
	}

	byCommand := map[string]string{}
	for _, op := range listing.Operations {
		byCommand[op.Command] = op.FullName
	}
	if byCommand["orders list"] != "listOrders" {
		t.Errorf("full_name = %q, want listOrders", byCommand["orders list"])
	}
	if byCommand["orders delete-by-id"] != "orders-delete-by-id" {
		t.Errorf("full_name = %q, want orders-delete-by-id", byCommand["orders delete-by-id"])
	}
}

func TestAddRoutes(t *testing.T) {
	api := &API{Title: "specless"}

	err := api.AddRoutes([]Route{
		{Name: "health", Method: "get", Path: "/healthz"},
		{Name: "reindex", Method: "POST", Path: "/admin/reindex/{tenant}"},
		{Name: "purge", Method: "DELETE", Path: "/admin/purge", Group: "admin"},
	})
	if err != nil {
		t.Fatalf("AddRoutes: %v", err)
	}

	if len(api.Operations) != 3 {
		t.Fatalf("operations = %d, want 3", len(api.Operations))
	}

	reindex := api.Find("reindex")
	if reindex == nil {
		t.Fatal("reindex is missing")
	}
	if reindex.Method != "POST" {
		t.Errorf("Method = %q, want it upper-cased", reindex.Method)
	}
	if params := reindex.PathParams(); len(params) != 1 || params[0].Name != "tenant" {
		t.Errorf("path params = %+v, want tenant", params)
	}
	if reindex.Source != SourceRoute {
		t.Errorf("Source = %q, want %q", reindex.Source, SourceRoute)
	}

	if purge := api.Find("admin-purge"); purge == nil || purge.Group != "admin" {
		t.Errorf("grouped route = %+v", purge)
	}
}

func TestAddRoutesRejectsCollisions(t *testing.T) {
	tests := []struct {
		name  string
		setup func() *API
		route Route
		want  string
	}{
		{
			name:  "reserved name at the top level",
			setup: func() *API { return &API{} },
			route: Route{Name: "spec", Method: "GET", Path: "/x"},
			want:  "collides with a blip command",
		},
		{
			name:  "reserved group",
			setup: func() *API { return &API{} },
			route: Route{Name: "x", Method: "GET", Path: "/x", Group: "version"},
			want:  "collides with a blip command",
		},
		{
			name:  "shadowing a generated command",
			setup: func() *API { return load(t, "dotnet9-minimal.json") },
			route: Route{Name: "get", Method: "GET", Path: "/x", Group: "orders"},
			want:  "collides with the generated command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.setup().AddRoutes([]Route{tt.route})
			if err == nil {
				t.Fatal("AddRoutes succeeded, want a config error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestRoutesAppearInTheCompactListing(t *testing.T) {
	api := &API{Title: "specless"}
	if err := api.AddRoutes([]Route{{Name: "health", Method: "GET", Path: "/healthz"}}); err != nil {
		t.Fatal(err)
	}

	lines := api.CompactLines()
	if len(lines) != 1 {
		t.Fatalf("lines = %v, want one", lines)
	}
	if !strings.Contains(lines[0], "health") || !strings.Contains(lines[0], "/healthz") {
		t.Errorf("line = %q", lines[0])
	}
	if !strings.Contains(lines[0], "(route)") {
		t.Errorf("line = %q, want declared routes marked", lines[0])
	}
}

func TestValidateResponse(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")
	get := find(t, api, "orders", "get")

	tests := []struct {
		name string
		body string
		want string
	}{
		{"conforming", `{"id":"7f000101-0000-4000-8000-000000000000","status":"open","total":1.5}`, ""},
		{"empty body is not a mismatch", ``, ""},
		{"wrong type", `{"total":"lots"}`, "/total"},
		{"not json at all", `<html>`, "was not JSON"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := get.ValidateResponse(200, []byte(tt.body))

			if tt.want == "" {
				if err != nil {
					t.Fatalf("ValidateResponse = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateResponse = nil, want an error mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestValidationMessageIsOneLine(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")
	get := find(t, api, "orders", "get")

	err := get.ValidateResponse(200, []byte(`{"id":12,"total":"lots","status":[]}`))
	if err == nil {
		t.Fatal("ValidateResponse = nil, want a mismatch")
	}

	if strings.Contains(err.Error(), "\n") {
		t.Errorf("message spans lines, which buries it in the diagnostics:\n%s", err)
	}
	if strings.Contains(err.Error(), "Schema:") {
		t.Errorf("message dumps the whole schema:\n%s", err)
	}
	if !strings.Contains(err.Error(), ";") {
		t.Errorf("message = %q, want the separate problems joined", err)
	}
}

func TestValidationIsSkippedWithNoDeclaredSchema(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")
	op := find(t, api, "healthz", "get")

	if err := op.ValidateResponse(200, []byte(`{"anything":true}`)); err != nil {
		t.Errorf("ValidateResponse = %v, want nil when the spec declares no schema", err)
	}
}
