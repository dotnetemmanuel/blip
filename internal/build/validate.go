package build

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// MaxReportedProblems bounds a validation message. A body that is wrong in
// thirty places is wrong; listing all thirty helps nobody.
const MaxReportedProblems = 3

// ValidateResponse checks a response body against the schema the spec declares
// for that status. It reports nil when the spec says nothing about the status,
// which is the common case and not a problem.
func (o *Operation) ValidateResponse(status int, body []byte) error {
	schema := o.responseSchema(status)
	if schema == nil || schema.Value == nil {
		return nil
	}
	if len(body) == 0 {
		return nil
	}

	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return fmt.Errorf("response to %s was not JSON, but the spec declares a schema for %d", o.FullName(), status)
	}

	err := schema.Value.VisitJSON(value, openapi3.MultiErrors())
	if err == nil {
		return nil
	}
	return fmt.Errorf("response to %s does not match the spec schema for %d: %s",
		o.FullName(), status, summarize(err))
}

func (o *Operation) responseSchema(status int) *openapi3.SchemaRef {
	if o.responses == nil {
		return nil
	}
	if schema, ok := o.responses[strconv.Itoa(status)]; ok {
		return schema
	}
	if schema, ok := o.responses[strconv.Itoa(status/100)+"XX"]; ok {
		return schema
	}
	if schema, ok := o.responses["default"]; ok {
		return schema
	}
	return nil
}

// summarize turns kin-openapi's multi-line report, which prints the whole schema
// and value, into one line per problem.
func summarize(err error) string {
	problems := flatten(err)
	if len(problems) == 0 {
		return err.Error()
	}

	shown := problems
	suffix := ""
	if len(shown) > MaxReportedProblems {
		suffix = fmt.Sprintf(" (and %d more)", len(shown)-MaxReportedProblems)
		shown = shown[:MaxReportedProblems]
	}
	return strings.Join(shown, "; ") + suffix
}

func flatten(err error) []string {
	switch e := err.(type) {
	case openapi3.MultiError:
		var out []string
		for _, inner := range e {
			out = append(out, flatten(inner)...)
		}
		return out

	case *openapi3.SchemaError:
		where := "/" + strings.Join(e.JSONPointer(), "/")
		if where == "/" {
			where = "body"
		}
		return []string{where + ": " + firstLine(e.Reason)}

	default:
		return []string{firstLine(err.Error())}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
