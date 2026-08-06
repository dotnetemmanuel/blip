package request

import (
	"encoding/json"
	"io"
	"os"
	"strings"
)

// StdinMarker is the --data value that means "read the body from stdin".
const StdinMarker = "@-"

// Data resolves a --data value: @file reads a file, @- reads stdin, anything else
// is the body itself.
func Data(value string, stdin io.Reader) ([]byte, error) {
	switch {
	case value == StdinMarker:
		if stdin == nil {
			return nil, usageError("--data %s was given but stdin is not readable", StdinMarker)
		}
		body, err := io.ReadAll(stdin)
		if err != nil {
			return nil, usageError("reading body from stdin: %v", err)
		}
		return body, nil

	case strings.HasPrefix(value, "@"):
		path := value[1:]
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, usageError("reading body from %s: %v", path, err)
		}
		return body, nil

	default:
		return []byte(value), nil
	}
}

// FieldValue is one body field with its name and its value already separated, so
// nothing downstream has to find the boundary again. Raw means the value is
// already JSON rather than a string to be quoted.
type FieldValue struct {
	Name  string
	Value string
	Raw   bool
}

// Fields builds a flat JSON object from repeated key=value pairs. A key ending in
// a colon (key:=value) takes its value as raw JSON, which is the only way to send
// a number, a boolean or null without guessing at what a string was meant to be.
func Fields(pairs []string) ([]byte, error) {
	values := make([]FieldValue, 0, len(pairs))

	for _, pair := range pairs {
		key, value, found := strings.Cut(pair, "=")
		if !found || key == "" {
			return nil, usageError("--field %q must be key=value (or key:=value for raw JSON)", pair)
		}

		raw := strings.HasSuffix(key, ":")
		if raw {
			key = strings.TrimSuffix(key, ":")
			if key == "" {
				return nil, usageError("--field %q has no key", pair)
			}
		}
		values = append(values, FieldValue{Name: key, Value: value, Raw: raw})
	}

	body, err := BuildFields(values)
	if err != nil {
		return nil, usageError("--field: %v", err)
	}
	return body, nil
}

// BuildFields is the one place a flat request body is encoded. Callers that
// already know a field's name and type hand it over split, so a name carrying a
// separator of its own cannot be cut in the wrong place.
func BuildFields(values []FieldValue) ([]byte, error) {
	object := make(map[string]any, len(values))

	for _, f := range values {
		if !f.Raw {
			object[f.Name] = f.Value
			continue
		}
		var parsed any
		if err := json.Unmarshal([]byte(f.Value), &parsed); err != nil {
			return nil, usageError("the value for %s is not valid JSON: %q", f.Name, f.Value)
		}
		object[f.Name] = parsed
	}

	body, err := json.Marshal(object)
	if err != nil {
		return nil, usageError("building the request body: %v", err)
	}
	return body, nil
}
