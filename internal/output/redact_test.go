package output

import (
	"net/http"
	"strings"
	"testing"
)

func TestHeaderValue(t *testing.T) {
	r := NewRedactor([]string{"tok-abc-9f2c1d"}, "X-Api-Key")

	tests := []struct {
		name   string
		header string
		value  string
		want   string
	}{
		{"bearer keeps the scheme", "Authorization", "Bearer tok-abc-9f2c1d", "Bearer " + Placeholder},
		{"basic keeps the scheme", "Authorization", "Basic dXNlcjpwdw==", "Basic " + Placeholder},
		{"opaque authorization is fully hidden", "Authorization", "tok-abc-9f2c1d", Placeholder},
		{"case does not matter", "authorization", "Bearer tok-abc-9f2c1d", "Bearer " + Placeholder},
		{"cookie", "Cookie", "session=1", Placeholder},
		{"set-cookie", "Set-Cookie", "session=1", Placeholder},
		{"proxy authorization", "Proxy-Authorization", "Basic x", "Basic " + Placeholder},
		{"configured secret header", "X-Api-Key", "tok-abc-9f2c1d", Placeholder},
		{"ordinary header survives", "Content-Type", "application/json", "application/json"},
		{"secret inside an ordinary header", "X-Trace", "id=tok-abc-9f2c1d", "id=" + Placeholder},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.HeaderValue(tt.header, tt.value); got != tt.want {
				t.Errorf("HeaderValue(%q, %q) = %q, want %q", tt.header, tt.value, got, tt.want)
			}
		})
	}
}

func TestStringReplacesEverySecret(t *testing.T) {
	r := NewRedactor([]string{"aaaaaaaa", "aaaaaaaabbbb"})

	got := r.String("prefix aaaaaaaabbbb middle aaaaaaaa suffix")

	if strings.Contains(got, "aaaaaaaa") {
		t.Errorf("String = %q, want every secret gone", got)
	}
	if want := "prefix " + Placeholder + " middle " + Placeholder + " suffix"; got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
}

func TestEmptySecretsAreIgnored(t *testing.T) {
	r := NewRedactor([]string{"", "real-secret-value"})

	if got := r.String("nothing to hide"); got != "nothing to hide" {
		t.Errorf("String = %q, want the input unchanged", got)
	}
}

func TestHeaderCopiesAndRedacts(t *testing.T) {
	r := NewRedactor([]string{"tok-9f2c1d88"})
	original := http.Header{"Authorization": {"Bearer tok-9f2c1d88"}, "Accept": {"application/json"}}

	got := r.Header(original)

	if original.Get("Authorization") != "Bearer tok-9f2c1d88" {
		t.Error("Header mutated the input")
	}
	if got.Get("Authorization") != "Bearer "+Placeholder {
		t.Errorf("Authorization = %q, want it redacted", got.Get("Authorization"))
	}
	if got.Get("Accept") != "application/json" {
		t.Errorf("Accept = %q, want it intact", got.Get("Accept"))
	}
}

func TestNilRedactorIsSafe(t *testing.T) {
	var r *Redactor

	if got := r.String("x"); got != "x" {
		t.Errorf("String = %q, want x", got)
	}
	if got := r.HeaderValue("Authorization", "Bearer x"); got != "Bearer x" {
		t.Errorf("HeaderValue = %q, want the value unchanged", got)
	}
}

func TestVeryShortSecretsAreNotSubstituted(t *testing.T) {
	// Redacting a one-character token would replace it everywhere, which shreds
	// the dry-run a human is asked to review and leaks the value through the
	// pattern of holes.
	r := NewRedactor([]string{"1", "sku", "abcdefg"})

	got := r.String(`POST http://127.0.0.1:8080/api/orders {"sku":"A1"}`)

	if got != `POST http://127.0.0.1:8080/api/orders {"sku":"A1"}` {
		t.Errorf("String = %q, want short values left alone", got)
	}
}

func TestASecretAtTheFloorIsStillRedacted(t *testing.T) {
	r := NewRedactor([]string{strings.Repeat("x", MinSecretLength)})

	if got := r.String("token " + strings.Repeat("x", MinSecretLength)); got != "token "+Placeholder {
		t.Errorf("String = %q, want the secret redacted at exactly the floor", got)
	}
}
