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

// Fields builds a flat JSON object from repeated key=value pairs. A key ending in
// a colon (key:=value) takes its value as raw JSON, which is the only way to send
// a number, a boolean or null without guessing at what a string was meant to be.
func Fields(pairs []string) ([]byte, error) {
	object := make(map[string]any, len(pairs))

	for _, pair := range pairs {
		key, value, found := strings.Cut(pair, "=")
		if !found || key == "" {
			return nil, usageError("--field %q must be key=value (or key:=value for raw JSON)", pair)
		}

		if strings.HasSuffix(key, ":") {
			key = strings.TrimSuffix(key, ":")
			if key == "" {
				return nil, usageError("--field %q has no key", pair)
			}
			var parsed any
			if err := json.Unmarshal([]byte(value), &parsed); err != nil {
				return nil, usageError("--field %s:= value %q is not valid JSON", key, value)
			}
			object[key] = parsed
		} else {
			object[key] = value
		}
	}

	body, err := json.Marshal(object)
	if err != nil {
		return nil, usageError("building body from --field: %v", err)
	}
	return body, nil
}
