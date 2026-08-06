package request

import (
	"strings"
	"testing"
)

func TestFieldsErrors(t *testing.T) {
	tests := []struct {
		name string
		pair string
		want string
	}{
		{name: "no separator", pair: "noequals", want: "must be key=value"},
		{name: "no key", pair: "=x", want: "must be key=value"},
		{name: "raw with no key", pair: ":=1", want: "has no key"},
		{name: "raw that is not JSON", pair: "qty:=notjson", want: "--field"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Fields([]string{tt.pair})
			if err == nil {
				t.Fatalf("Fields(%q) was accepted, want an error", tt.pair)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestFieldsSplitsAtTheFirstSeparatorOnly(t *testing.T) {
	body, err := Fields([]string{"note=a=b"})
	if err != nil {
		t.Fatalf("Fields returned %v", err)
	}
	if got := string(body); got != `{"note":"a=b"}` {
		t.Errorf("body = %s, want only the first = to separate", got)
	}
}

// BuildFields takes the name already split off, so a name carrying a separator
// of its own reaches the body whole.
func TestBuildFieldsKeepsANameCarryingASeparator(t *testing.T) {
	body, err := BuildFields([]FieldValue{
		{Name: "a=b", Value: "c"},
		{Name: "weird:", Value: "1", Raw: true},
	})
	if err != nil {
		t.Fatalf("BuildFields returned %v", err)
	}
	if got := string(body); got != `{"a=b":"c","weird:":1}` {
		t.Errorf("body = %s, want both names kept whole", got)
	}
}

func TestBuildFieldsRefusesRawThatIsNotJSON(t *testing.T) {
	_, err := BuildFields([]FieldValue{{Name: "qty", Value: "notjson", Raw: true}})
	if err == nil {
		t.Fatal("invalid JSON was accepted, want it refused")
	}
	if got := err.Error(); !strings.Contains(got, "qty") {
		t.Errorf("error = %q, want it to name the field", got)
	}
	if got := err.Error(); strings.Contains(got, "--field") {
		t.Errorf("error = %q, want the shared encoder not to name a flag; only Fields adds that", got)
	}
}
