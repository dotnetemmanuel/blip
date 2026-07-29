package request

import (
	"net/http"
	"net/url"
	"strings"
)

// ParseHeaders reads repeated --header values. Both "Name: value" and "Name=value"
// are accepted, because both are what people already have in their fingers.
func ParseHeaders(values []string) (http.Header, error) {
	header := http.Header{}
	for _, raw := range values {
		name, value, found := strings.Cut(raw, ":")
		if !found {
			name, value, found = strings.Cut(raw, "=")
		}
		name = strings.TrimSpace(name)
		if !found || name == "" {
			return nil, usageError("--header %q must be \"Name: value\"", raw)
		}
		header.Add(name, strings.TrimSpace(value))
	}
	return header, nil
}

// ParseQuery reads repeated --query values of the form name=value.
func ParseQuery(values []string) (url.Values, error) {
	query := url.Values{}
	for _, raw := range values {
		name, value, found := strings.Cut(raw, "=")
		if !found || name == "" {
			return nil, usageError("--query %q must be name=value", raw)
		}
		query.Add(name, value)
	}
	return query, nil
}
