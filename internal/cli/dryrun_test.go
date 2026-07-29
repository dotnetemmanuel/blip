package cli

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotnetemmanuel/blip/internal/output"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// goldenFile pins output an agent is expected to parse. --dry-run is a contract:
// it should not change shape without someone deciding that it should.
func goldenFile(t *testing.T, name, got string) {
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
		t.Fatalf("reading golden file: %v (run: go test ./internal/cli -update)", err)
	}
	if got != string(want) {
		t.Errorf("output does not match %s:\n--- got ---\n%s--- want ---\n%s", path, got, want)
	}
}

func TestDryRunGolden(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		golden string
	}{
		{
			name:   "generated mutation with a field body",
			args:   []string{"orders", "create", "--field", "sku=A1", "--field", "quantity:=2", "--dry-run"},
			golden: "dry-run-create.txt",
		},
		{
			name:   "generated read with query and header",
			args:   []string{"orders", "get", "42", "--header-x-tenant", "acme", "--dry-run"},
			golden: "dry-run-get.txt",
		},
		{
			name:   "raw request",
			args:   []string{"raw", "DELETE", "/api/orders/9", "--dry-run"},
			golden: "dry-run-raw.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newAPIStub(t, "dotnet9-minimal.json")
			f := apiFixture(t, srv, "auth = \"orders-dev\"\n")
			f.writeCredentials(t, bearerCreds)

			got := f.run(t, tt.args...)

			if got.code != output.ExitOK {
				t.Fatalf("exit = %d (%s)", got.code, got.stderr)
			}
			// The stub's port moves every run; the contract is the shape.
			goldenFile(t, tt.golden, strings.ReplaceAll(got.stdout, srv.URL, "http://api.test"))
		})
	}
}
