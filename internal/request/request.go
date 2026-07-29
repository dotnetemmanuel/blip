package request

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/dotnetemmanuel/blip/internal/output"
)

// Request is a resolved call, before any base URL or credential is applied.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
}

// Response is what came back, with the body already read.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

func usageError(format string, args ...any) error {
	return output.Usagef(format, args...)
}

// ResolveURL applies a path and query to a base URL. A path prefix on the base
// URL is kept, so a base of https://host/api and a path of /orders give
// https://host/api/orders.
func ResolveURL(base *url.URL, rawPath string, query url.Values) (*url.URL, error) {
	if !strings.HasPrefix(rawPath, "/") {
		return nil, usageError("path %q must start with /", rawPath)
	}

	ref, err := url.Parse(rawPath)
	if err != nil {
		return nil, usageError("path %q is not a valid URL path: %v", rawPath, err)
	}

	resolved := *base
	resolved.Path = strings.TrimSuffix(base.Path, "/") + ref.Path

	// Keep the encoded form when it differs, or an escaped path argument would be
	// decoded back into separators and could address a different endpoint.
	escaped := strings.TrimSuffix(base.EscapedPath(), "/") + ref.EscapedPath()
	resolved.RawPath = ""
	if escaped != resolved.Path {
		resolved.RawPath = escaped
	}

	merged := base.Query()
	for key, values := range ref.Query() {
		merged[key] = append(merged[key], values...)
	}
	for key, values := range query {
		merged[key] = append(merged[key], values...)
	}
	resolved.RawQuery = merged.Encode()

	return &resolved, nil
}

// HTTPRequest turns a Request into something that can be sent.
func (r *Request) HTTPRequest(ctx context.Context, base *url.URL) (*http.Request, error) {
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	if method == "" {
		return nil, usageError("no HTTP method given")
	}
	if strings.ContainsAny(method, " \t/") {
		return nil, usageError("%q is not an HTTP method", r.Method)
	}

	u, err := ResolveURL(base, r.Path, r.Query)
	if err != nil {
		return nil, err
	}

	var body io.Reader
	if len(r.Body) > 0 {
		body = bytes.NewReader(r.Body)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, usageError("building request: %v", err)
	}
	for name, values := range r.Header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if len(r.Body) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// Do sends a request. Every failure to get a response at all is a transport error,
// which is a different kind of problem from a response blip does not like.
func Do(client *http.Client, req *http.Request) (*Response, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, output.WithCode(transportMessage(err), output.ExitTransport)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, output.WithCode(fmt.Errorf("reading response body: %w", err), output.ExitTransport)
	}

	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: body}, nil
}

func transportMessage(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Timeout() {
			return fmt.Errorf("%s %s timed out", urlErr.Op, urlErr.URL)
		}
		return fmt.Errorf("%s %s failed: %w", urlErr.Op, urlErr.URL, urlErr.Err)
	}
	return err
}
