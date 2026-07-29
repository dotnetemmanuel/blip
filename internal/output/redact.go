package output

import (
	"net/http"
	"sort"
	"strings"
)

// Placeholder is what replaces a secret in any output blip produces.
const Placeholder = "<redacted>"

// MinSecretLength is the shortest value worth substituting. Anything shorter
// matches so much unrelated text that the output stops being reviewable, and the
// pattern of holes leaks the value it was hiding.
const MinSecretLength = 8

// alwaysRedacted are headers whose value is secret whatever the profile says.
var alwaysRedacted = []string{
	"Authorization",
	"Proxy-Authorization",
	"Cookie",
	"Set-Cookie",
}

// Redactor keeps secrets out of verbose and dry-run output.
type Redactor struct {
	headers map[string]bool
	secrets []string
}

// NewRedactor redacts the always-secret headers, any extra header the profile
// names, and the literal secret values themselves wherever they appear.
func NewRedactor(secrets []string, extraHeaders ...string) *Redactor {
	r := &Redactor{headers: map[string]bool{}}
	for _, h := range alwaysRedacted {
		r.headers[http.CanonicalHeaderKey(h)] = true
	}
	for _, h := range extraHeaders {
		if h != "" {
			r.headers[http.CanonicalHeaderKey(h)] = true
		}
	}
	for _, s := range secrets {
		if len(s) >= MinSecretLength {
			r.secrets = append(r.secrets, s)
		}
	}
	// Longest first, so a secret that contains another is replaced whole.
	sort.Slice(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
	return r
}

// HeaderValue redacts a header value, keeping the auth scheme visible because
// knowing it is Bearer rather than Basic is diagnostic, not secret.
func (r *Redactor) HeaderValue(name, value string) string {
	if r == nil {
		return value
	}
	if !r.headers[http.CanonicalHeaderKey(name)] {
		return r.String(value)
	}
	if scheme, _, found := strings.Cut(value, " "); found && isAuthScheme(scheme) {
		return scheme + " " + Placeholder
	}
	return Placeholder
}

// String replaces any known secret value wherever it appears.
func (r *Redactor) String(s string) string {
	if r == nil {
		return s
	}
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, Placeholder)
	}
	return s
}

// Header returns a copy of h with every value redacted.
func (r *Redactor) Header(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for name, values := range h {
		for _, v := range values {
			out.Add(name, r.HeaderValue(name, v))
		}
	}
	return out
}

func isAuthScheme(s string) bool {
	switch strings.ToLower(s) {
	case "bearer", "basic", "digest", "negotiate", "ntlm":
		return true
	}
	return false
}
