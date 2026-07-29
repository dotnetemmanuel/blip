package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// Output modes.
const (
	ModeJSON   = "json"
	ModeRaw    = "raw"
	ModeStatus = "status"
)

// ValidateMode rejects an unknown --output value.
func ValidateMode(mode string) error {
	switch mode {
	case ModeJSON, ModeRaw, ModeStatus:
		return nil
	}
	return WithCode(fmt.Errorf("unknown --output %q (want json, raw or status)", mode), ExitUsage)
}

// Renderer writes a response according to the output contract: body on stdout,
// everything else on stderr.
type Renderer struct {
	Mode           string
	Pretty         bool
	IncludeHeaders bool
	Stdout         io.Writer
	Stderr         io.Writer
	Redactor       *Redactor
}

// Response writes the response. Error bodies are written just like any other, so
// the API's own error payload survives.
func (r *Renderer) Response(status int, header http.Header, body []byte) error {
	if r.IncludeHeaders {
		r.writeHeaders(status, header)
	}

	switch r.Mode {
	case ModeStatus:
		_, err := fmt.Fprintln(r.Stdout, status)
		return err
	case ModeRaw:
		_, err := r.Stdout.Write(body)
		return err
	default:
		return r.writeJSON(body)
	}
}

func (r *Renderer) writeJSON(body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}

	var buf bytes.Buffer
	var err error
	if r.Pretty {
		err = json.Indent(&buf, body, "", "  ")
	} else {
		err = json.Compact(&buf, body)
	}
	if err != nil {
		// Not JSON. Hand it over untouched rather than mangling or hiding it.
		_, err := r.Stdout.Write(body)
		return err
	}

	buf.WriteByte('\n')
	_, err = r.Stdout.Write(buf.Bytes())
	return err
}

func (r *Renderer) writeHeaders(status int, header http.Header) {
	fmt.Fprintf(r.Stderr, "HTTP %d\n", status)
	for _, name := range sortedNames(header) {
		for _, v := range header[name] {
			fmt.Fprintf(r.Stderr, "%s: %s\n", name, r.Redactor.HeaderValue(name, v))
		}
	}
}

// Summary is the one-line stderr note that accompanies a non-2xx response.
func (r *Renderer) Summary(method, url string, status int) {
	fmt.Fprintf(r.Stderr, "blip: %s %s -> %d %s\n", method, r.Redactor.String(url), status, http.StatusText(status))
}

func sortedNames(header http.Header) []string {
	names := make([]string, 0, len(header))
	for name := range header {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ExitCodeForStatus maps an HTTP status onto blip's exit-code contract. Anything
// that is not 2xx, 401, 403 or 5xx is reported as a client error, including a 3xx
// that survived redirect handling.
func ExitCodeForStatus(status int) int {
	switch {
	case status >= 200 && status < 300:
		return ExitOK
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ExitAuth
	case status >= 500:
		return ExitServer
	default:
		return ExitClient
	}
}

// DryRun renders a request without sending it. The format is a contract an agent
// reads, so it stays stable: request line, environment, headers, blank line, body.
func (r *Renderer) DryRun(method, url, envName string, header http.Header, body []byte) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", strings.ToUpper(method), r.Redactor.String(url))
	fmt.Fprintf(&b, "env: %s\n", envName)
	for _, name := range sortedNames(header) {
		for _, v := range header[name] {
			fmt.Fprintf(&b, "%s: %s\n", name, r.Redactor.HeaderValue(name, v))
		}
	}
	if len(body) > 0 {
		b.WriteString("\n")
		b.Write([]byte(r.Redactor.String(string(body))))
		if !bytes.HasSuffix(body, []byte("\n")) {
			b.WriteString("\n")
		}
	}

	_, err := io.WriteString(r.Stdout, b.String())
	return err
}
