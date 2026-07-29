package request

import (
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

// ParseHeaders reads repeated --header values. Both "Name: value" and "Name=value"
// are accepted, because both are what people already have in their fingers.
func ParseHeaders(values []string) (http.Header, error) {
	header := http.Header{}
	for _, raw := range values {
		// Whichever separator comes first wins, so a value that is itself a URL
		// does not get cut at its scheme.
		sep := strings.IndexAny(raw, ":=")
		if sep <= 0 {
			return nil, usageError("--header %q must be \"Name: value\"", raw)
		}
		name := strings.TrimSpace(raw[:sep])
		value := strings.TrimSpace(raw[sep+1:])
		if !validHeaderName(name) {
			return nil, usageError("--header %q has an invalid header name %q", raw, name)
		}
		header.Add(name, value)
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

// validHeaderName keeps a malformed name out of the transport, where it would
// surface as a confusing transport error rather than the usage error it is.
func validHeaderName(name string) bool {
	return name != "" && textproto.TrimString(name) == name && !strings.ContainsAny(name, " \t\"(),/;<=>?@[\\]{}")
}
