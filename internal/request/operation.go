package request

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/dotnetemmanuel/blip/internal/build"
)

// OperationValues is what a caller resolved for an operation, before any
// encoding decision has been made. Path holds one value per
// Operation.PathParams(), in that order; Query and Header are keyed by the
// parameter name the spec uses, not by whatever the caller called it.
type OperationValues struct {
	Path   []string
	Query  url.Values
	Header http.Header
	Body   []byte
}

// Missing is what an operation needs and the values did not supply. It names
// parameters rather than wording a complaint, because the CLI reached them as
// flags and the explorer reached them as fields, and each has to say so its own
// way.
type Missing struct {
	Params []build.Param
	Body   bool
}

func (m Missing) Any() bool {
	return len(m.Params) > 0 || m.Body
}

// ForOperation assembles the request an operation describes. Every front end
// goes through here, so path escaping and the content type a body carries
// cannot come out differently depending on how the values were typed.
func ForOperation(o *build.Operation, v OperationValues) (*Request, error) {
	pathParams := o.PathParams()
	if len(v.Path) != len(pathParams) {
		return nil, usageError("%s takes %d path value(s), got %d", o.Path, len(pathParams), len(v.Path))
	}

	path := o.Path
	for i, p := range pathParams {
		path = strings.Replace(path, "{"+p.Name+"}", url.PathEscape(v.Path[i]), 1)
	}

	req := &Request{
		Method: o.Method,
		Path:   path,
		Query:  url.Values{},
		Header: http.Header{},
		Body:   v.Body,
	}
	for name, values := range v.Query {
		for _, value := range values {
			req.Query.Add(name, value)
		}
	}
	for name, values := range v.Header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}

	if o.Body != nil && o.Body.ContentType != "" && len(v.Body) > 0 {
		req.Header.Set("Content-Type", o.Body.ContentType)
	}
	return req, nil
}

// NormalizeScalar puts a typed value into the one spelling blip sends, so that
// 007, +5 and T do not reach a server as themselves from one front end and as 7,
// 5 and true from another. The result is always valid JSON for its type, which is
// what lets a body field carry it raw. A type blip does not narrow, a string or
// anything unrecognised, is returned untouched.
func NormalizeScalar(typ, value string) (string, error) {
	switch typ {
	case build.TypeInteger:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return "", fmt.Errorf("must be a whole number, got %q", value)
		}
		return strconv.FormatInt(n, 10), nil
	case build.TypeNumber:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return "", fmt.Errorf("must be a number, got %q", value)
		}
		return strconv.FormatFloat(f, 'g', -1, 64), nil
	case build.TypeBoolean:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return "", fmt.Errorf("must be true or false, got %q", value)
		}
		return strconv.FormatBool(b), nil
	default:
		return value, nil
	}
}

// MissingFrom reports what the operation requires and the values leave out,
// walking path then query then header so the answer is the same on every run.
func MissingFrom(o *build.Operation, v OperationValues) Missing {
	var missing Missing

	for i, p := range o.PathParams() {
		if i >= len(v.Path) || v.Path[i] == "" {
			missing.Params = append(missing.Params, p)
		}
	}
	for _, p := range o.ParamsIn(build.InQuery) {
		if p.Required && len(v.Query[p.Name]) == 0 {
			missing.Params = append(missing.Params, p)
		}
	}
	for _, p := range o.ParamsIn(build.InHeader) {
		if p.Required && len(v.Header.Values(p.Name)) == 0 {
			missing.Params = append(missing.Params, p)
		}
	}
	if o.Body != nil && o.Body.Required && len(v.Body) == 0 {
		missing.Body = true
	}
	return missing
}
