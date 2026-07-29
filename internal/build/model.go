// Package build turns an OpenAPI document into a cobra command tree.
package build

import (
	"github.com/getkin/kin-openapi/openapi3"
)

// Parameter kinds, matching the spec's "in" values blip supports.
const (
	InPath   = "path"
	InQuery  = "query"
	InHeader = "header"
)

// Value types blip maps spec schemas onto.
const (
	TypeString  = "string"
	TypeInteger = "integer"
	TypeNumber  = "number"
	TypeBoolean = "boolean"
	TypeArray   = "array"
)

// Param is one path, query or header parameter.
type Param struct {
	Name        string   `json:"name"`
	In          string   `json:"in"`
	Required    bool     `json:"required"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type"`
	ItemType    string   `json:"item_type,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     string   `json:"default,omitempty"`
}

// Field is one property of a flat request body.
type Field struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// Body describes a request body. Only flat objects get --field; anything nested
// is --data only, deliberately.
type Body struct {
	Required    bool    `json:"required"`
	ContentType string  `json:"content_type"`
	Schema      string  `json:"schema,omitempty"`
	Flat        bool    `json:"flat"`
	Fields      []Field `json:"fields,omitempty"`
}

// Operation is one callable endpoint.
type Operation struct {
	ID          string  `json:"id,omitempty"`
	Group       string  `json:"group"`
	Name        string  `json:"name"`
	Method      string  `json:"method"`
	Path        string  `json:"path"`
	Summary     string  `json:"summary,omitempty"`
	Description string  `json:"description,omitempty"`
	Deprecated  bool    `json:"deprecated,omitempty"`
	Params      []Param `json:"params,omitempty"`
	Body        *Body   `json:"body,omitempty"`

	// Derived is true when the spec gave no operationId and blip made a name up.
	Derived bool `json:"derived"`

	// Source says where the operation came from: the spec, or a [[route]] entry.
	Source string `json:"source"`

	responses map[string]*openapi3.SchemaRef
}

// Sources an operation can come from.
const (
	SourceSpec  = "spec"
	SourceRoute = "route"
)

// FullName is the stable address of an operation: its operationId when it has
// one, otherwise group and name joined.
func (o *Operation) FullName() string {
	if o.ID != "" {
		return o.ID
	}
	if o.Group == "" {
		return o.Name
	}
	return o.Group + "-" + o.Name
}

// PathParams returns the path parameters in the order they appear in the path.
func (o *Operation) PathParams() []Param {
	var params []Param
	for _, name := range pathParamNames(o.Path) {
		for _, p := range o.Params {
			if p.In == InPath && p.Name == name {
				params = append(params, p)
			}
		}
	}
	return params
}

// ParamsIn returns the parameters of one kind, in spec order.
func (o *Operation) ParamsIn(in string) []Param {
	var params []Param
	for _, p := range o.Params {
		if p.In == in {
			params = append(params, p)
		}
	}
	return params
}

// API is a whole parsed document.
type API struct {
	Title      string       `json:"title"`
	Version    string       `json:"version"`
	Operations []*Operation `json:"operations"`

	// Warnings are things the user should fix upstream, reported once.
	Warnings []string `json:"-"`
}

// Groups lists the group names in the order they should appear.
func (a *API) Groups() []string {
	var groups []string
	seen := map[string]bool{}
	for _, op := range a.Operations {
		if !seen[op.Group] {
			seen[op.Group] = true
			groups = append(groups, op.Group)
		}
	}
	return groups
}

// InGroup returns the operations of one group.
func (a *API) InGroup(group string) []*Operation {
	var ops []*Operation
	for _, op := range a.Operations {
		if op.Group == group {
			ops = append(ops, op)
		}
	}
	return ops
}

// Find looks an operation up by its stable name, then by group and name.
func (a *API) Find(name string) *Operation {
	for _, op := range a.Operations {
		if op.FullName() == name {
			return op
		}
	}
	for _, op := range a.Operations {
		if op.Group+" "+op.Name == name || op.Name == name {
			return op
		}
	}
	return nil
}
