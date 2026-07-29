package build

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/getkin/kin-openapi/openapi3"
)

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
	if err := schema.Value.VisitJSON(value); err != nil {
		return fmt.Errorf("response to %s does not match the spec schema for %d: %w", o.FullName(), status, err)
	}
	return nil
}

// HasResponseSchema reports whether the spec describes the body for a status.
func (o *Operation) HasResponseSchema(status int) bool {
	return o.responseSchema(status) != nil
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
