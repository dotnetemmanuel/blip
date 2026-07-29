package output

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

func newRenderer(mode string, pretty bool) (*Renderer, *bytes.Buffer, *bytes.Buffer) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	return &Renderer{
		Mode:     mode,
		Pretty:   pretty,
		Stdout:   stdout,
		Stderr:   stderr,
		Redactor: NewRedactor(nil),
	}, stdout, stderr
}

func TestResponseModes(t *testing.T) {
	body := []byte("{\n  \"id\": 7,\n  \"status\": \"open\"\n}")

	tests := []struct {
		name   string
		mode   string
		pretty bool
		want   string
	}{
		{"json compact when piped", ModeJSON, false, "{\"id\":7,\"status\":\"open\"}\n"},
		{"json pretty at a terminal", ModeJSON, true, "{\n  \"id\": 7,\n  \"status\": \"open\"\n}\n"},
		{"raw is untouched", ModeRaw, false, string(body)},
		{"status is the code alone", ModeStatus, false, "200\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, stdout, stderr := newRenderer(tt.mode, tt.pretty)

			if err := r.Response(200, http.Header{}, body); err != nil {
				t.Fatalf("Response: %v", err)
			}
			if got := stdout.String(); got != tt.want {
				t.Errorf("stdout = %q, want %q", got, tt.want)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestResponseKeepsNonJSONBodyIntact(t *testing.T) {
	r, stdout, _ := newRenderer(ModeJSON, true)
	body := []byte("<html>not json</html>")

	if err := r.Response(200, http.Header{}, body); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != string(body) {
		t.Errorf("stdout = %q, want the body untouched", got)
	}
}

func TestResponseWithAnEmptyBodyWritesNothing(t *testing.T) {
	r, stdout, _ := newRenderer(ModeJSON, true)

	if err := r.Response(204, http.Header{}, nil); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestIncludeHeadersGoesToStderr(t *testing.T) {
	r, stdout, stderr := newRenderer(ModeJSON, false)
	r.IncludeHeaders = true
	header := http.Header{
		"Content-Type": {"application/json"},
		"Set-Cookie":   {"session=abc123"},
	}

	if err := r.Response(200, header, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}

	if got := stdout.String(); got != "{\"a\":1}\n" {
		t.Errorf("stdout = %q, want only the body", got)
	}
	if !strings.Contains(stderr.String(), "HTTP 200") {
		t.Errorf("stderr = %q, want the status line", stderr.String())
	}
	if strings.Contains(stderr.String(), "session=abc123") {
		t.Errorf("stderr = %q, want Set-Cookie redacted", stderr.String())
	}
}

func TestExitCodeForStatus(t *testing.T) {
	tests := []struct {
		status int
		want   int
	}{
		{200, ExitOK},
		{201, ExitOK},
		{204, ExitOK},
		{301, ExitClient},
		{304, ExitClient},
		{400, ExitClient},
		{401, ExitAuth},
		{403, ExitAuth},
		{404, ExitClient},
		{422, ExitClient},
		{500, ExitServer},
		{503, ExitServer},
	}

	for _, tt := range tests {
		if got := ExitCodeForStatus(tt.status); got != tt.want {
			t.Errorf("ExitCodeForStatus(%d) = %d, want %d", tt.status, got, tt.want)
		}
	}
}

func TestValidateMode(t *testing.T) {
	for _, mode := range []string{ModeJSON, ModeRaw, ModeStatus} {
		if err := ValidateMode(mode); err != nil {
			t.Errorf("ValidateMode(%q) = %v, want nil", mode, err)
		}
	}

	err := ValidateMode("yaml")
	if err == nil {
		t.Fatal("ValidateMode(yaml) = nil, want a usage error")
	}
	if got := ExitCodeFor(err); got != ExitUsage {
		t.Errorf("exit code = %d, want %d", got, ExitUsage)
	}
}

func TestDryRunIsStableAndRedacted(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	r := &Renderer{
		Stdout:   stdout,
		Stderr:   stderr,
		Redactor: NewRedactor([]string{"super-secret"}, "X-Api-Key"),
	}
	header := http.Header{
		"Authorization": {"Bearer super-secret"},
		"X-Api-Key":     {"super-secret"},
		"Content-Type":  {"application/json"},
		"Accept":        {"application/json"},
	}

	if err := r.DryRun("post", "https://api.test/orders", "dev", header, []byte(`{"sku":"A1"}`)); err != nil {
		t.Fatal(err)
	}

	want := strings.Join([]string{
		"POST https://api.test/orders",
		"env: dev",
		"Accept: application/json",
		"Authorization: Bearer <redacted>",
		"Content-Type: application/json",
		"X-Api-Key: <redacted>",
		"",
		`{"sku":"A1"}`,
		"",
	}, "\n")

	if got := stdout.String(); got != want {
		t.Errorf("dry run output:\n%s\nwant:\n%s", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestDryRunRedactsASecretInTheBody(t *testing.T) {
	stdout := &bytes.Buffer{}
	r := &Renderer{Stdout: stdout, Stderr: &bytes.Buffer{}, Redactor: NewRedactor([]string{"pw-123"})}

	if err := r.DryRun("POST", "https://api.test/login", "dev", http.Header{}, []byte(`{"password":"pw-123"}`)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "pw-123") {
		t.Errorf("dry run leaked a secret from the body:\n%s", stdout.String())
	}
}

func TestSummaryGoesToStderr(t *testing.T) {
	r, stdout, stderr := newRenderer(ModeJSON, false)

	r.Summary("GET", "https://api.test/x", 503)

	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "503") {
		t.Errorf("stderr = %q, want the status", stderr.String())
	}
}
