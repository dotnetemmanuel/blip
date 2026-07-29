package safety

import (
	"errors"
	"strings"
	"testing"

	"github.com/dotnetemmanuel/blip/internal/output"
)

func TestCheck(t *testing.T) {
	yes := func(string) (bool, error) { return true, nil }
	no := func(string) (bool, error) { return false, nil }

	tests := []struct {
		name    string
		gate    Gate
		method  string
		wantErr string
	}{
		{"read in a readonly env", Gate{Readonly: true}, "GET", ""},
		{"head in a readonly env", Gate{Readonly: true}, "HEAD", ""},
		{"options in a readonly env", Gate{Readonly: true}, "OPTIONS", ""},
		{"write in a readonly env", Gate{Readonly: true}, "POST", "readonly"},
		{"delete in a readonly env", Gate{Readonly: true}, "DELETE", "readonly"},
		{"yes cannot override readonly", Gate{Readonly: true, Yes: true}, "PATCH", "readonly"},
		{"unknown verb in a readonly env", Gate{Readonly: true}, "PURGE", "readonly"},
		{"write with no tty and no yes", Gate{}, "POST", "needs --yes"},
		{"write with no tty and yes", Gate{Yes: true}, "POST", ""},
		{"write confirmed at a terminal", Gate{Interactive: true, Confirm: yes}, "PUT", ""},
		{"write declined at a terminal", Gate{Interactive: true, Confirm: no}, "PUT", "declined"},
		{"yes skips the prompt", Gate{Interactive: true, Yes: true, Confirm: no}, "PUT", ""},
		{"read needs nothing", Gate{}, "GET", ""},
		{"dry run needs no yes", Gate{DryRun: true}, "POST", ""},
		{"dry run is not prompted", Gate{DryRun: true, Interactive: true, Confirm: no}, "POST", ""},
		{"dry run cannot enter a readonly env", Gate{DryRun: true, Readonly: true}, "POST", "readonly"},
		{"lowercase verb is still a mutation", Gate{}, "post", "needs --yes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.gate.Check(tt.method, "https://api.test/x", "dev")

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Check = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Check = nil, want an error mentioning %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %q, want it to mention %q", err, tt.wantErr)
			}
			if got := output.ExitCodeFor(err); got != output.ExitBlocked {
				t.Errorf("exit code = %d, want %d", got, output.ExitBlocked)
			}
		})
	}
}

func TestCheckWithoutAConfirmerNeverPrompts(t *testing.T) {
	err := Gate{Interactive: true}.Check("POST", "https://api.test/x", "dev")

	if err == nil {
		t.Fatal("Check = nil, want a block when there is no way to ask")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("err = %q, want it to name --yes", err)
	}
}

func TestConfirmerErrorBlocks(t *testing.T) {
	gate := Gate{Interactive: true, Confirm: func(string) (bool, error) {
		return true, errors.New("terminal closed")
	}}

	err := gate.Check("DELETE", "https://api.test/x", "dev")

	if err == nil {
		t.Fatal("Check = nil, want a block when confirmation could not be read")
	}
	if got := output.ExitCodeFor(err); got != output.ExitBlocked {
		t.Errorf("exit code = %d, want %d", got, output.ExitBlocked)
	}
}

func TestBlockMessageNamesTheRequest(t *testing.T) {
	err := Gate{}.Check("POST", "https://api.test/orders", "prod")

	for _, want := range []string{"POST", "https://api.test/orders", "prod"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
}
