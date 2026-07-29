package build

import (
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/dotnetemmanuel/blip/internal/output"
)

// JSONContentType is the body type blip generates flags for.
const JSONContentType = "application/json"

// methodOrder keeps the command tree stable regardless of map iteration.
var methodOrder = []string{
	http.MethodGet,
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
	http.MethodHead,
	http.MethodOptions,
	http.MethodTrace,
}

// Parse turns a raw OpenAPI document into blip's model. Operations are visited
// in a fixed order, so a derived name never silently changes between runs.
func Parse(data []byte) (*API, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false

	doc, err := loader.LoadFromData(data)
	if err != nil {
		return nil, output.WithCode(fmt.Errorf("parsing the spec: %w", err), output.ExitConfig)
	}

	api := &API{}
	if doc.Info != nil {
		api.Title = doc.Info.Title
		api.Version = doc.Info.Version
	}
	if doc.Paths == nil {
		return api, nil
	}

	groups := map[string]deduplicator{}
	renamedGroups := map[string]string{}
	var derived []string

	for _, p := range sortedPaths(doc.Paths) {
		item := doc.Paths.Find(p)
		if item == nil {
			continue
		}
		for _, method := range methodOrder {
			specOp := item.GetOperation(method)
			if specOp == nil {
				continue
			}

			group := groupFor(specOp.Tags, p, renamedGroups, &api.Warnings)
			if groups[group] == nil {
				groups[group] = deduplicator{}
			}

			op := &Operation{
				ID:          specOp.OperationID,
				Group:       group,
				Method:      method,
				Path:        p,
				Summary:     specOp.Summary,
				Description: specOp.Description,
				Deprecated:  specOp.Deprecated,
				Params:      params(item.Parameters, specOp.Parameters),
				Body:        requestBody(specOp.RequestBody),
				Source:      SourceSpec,
				responses:   responseSchemas(specOp.Responses),
			}

			base := ""
			if specOp.OperationID != "" {
				base = shortName(kebab(specOp.OperationID), group)
			} else {
				op.Derived = true
				base = shortName(deriveName(method, p), group)
				derived = append(derived, method+" "+p)
			}
			op.Name = groups[group].unique(base)

			api.Operations = append(api.Operations, op)
		}
	}

	if len(derived) > 0 {
		api.Warnings = append(api.Warnings, fmt.Sprintf(
			"%d operations have no operationId, so blip derived names for them: %s. Add .WithName() and .WithTags() upstream to control them",
			len(derived), strings.Join(derived, ", ")))
	}

	sortOperations(api.Operations)
	return api, nil
}

func sortedPaths(paths *openapi3.Paths) []string {
	keys := paths.Keys()
	sort.Strings(keys)
	return keys
}

// sortOperations puts the tree in a stable, readable order: group, then path,
// then method.
func sortOperations(ops []*Operation) {
	sort.SliceStable(ops, func(i, j int) bool {
		a, b := ops[i], ops[j]
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return methodRank(a.Method) < methodRank(b.Method)
	})
}

func methodRank(method string) int {
	for i, m := range methodOrder {
		if m == method {
			return i
		}
	}
	return len(methodOrder)
}

func groupFor(tags []string, p string, renamed map[string]string, warnings *[]string) string {
	group := deriveGroup(tags, p)
	if !Reserved[group] {
		return group
	}
	if already, ok := renamed[group]; ok {
		return already
	}
	safe := group + "-api"
	renamed[group] = safe
	*warnings = append(*warnings, fmt.Sprintf(
		"group %q collides with a blip command, so it is exposed as %q; blip call <operationId> is unaffected", group, safe))
	return safe
}

// params merges the path-item parameters, which apply to every operation, with
// the operation's own.
func params(shared, own openapi3.Parameters) []Param {
	seen := map[string]bool{}
	var out []Param

	for _, set := range []openapi3.Parameters{own, shared} {
		for _, ref := range set {
			if ref == nil || ref.Value == nil {
				continue
			}
			p := ref.Value
			key := p.In + ":" + p.Name
			if seen[key] || (p.In != InPath && p.In != InQuery && p.In != InHeader) {
				continue
			}
			seen[key] = true

			param := Param{
				Name:        p.Name,
				In:          p.In,
				Required:    p.Required || p.In == InPath,
				Description: p.Description,
				Type:        TypeString,
			}
			if p.Schema != nil && p.Schema.Value != nil {
				s := p.Schema.Value
				param.Type = schemaType(s)
				param.Enum = enumStrings(s.Enum)
				if s.Default != nil {
					param.Default = fmt.Sprint(s.Default)
				}
				if param.Type == TypeArray && s.Items != nil && s.Items.Value != nil {
					param.ItemType = schemaType(s.Items.Value)
				}
			}
			out = append(out, param)
		}
	}

	// Grouped by kind, but spec order within a kind: the author put the useful
	// parameters first, and that ordering is already deterministic.
	sort.SliceStable(out, func(i, j int) bool {
		return inRank(out[i].In) < inRank(out[j].In)
	})
	return out
}

func inRank(in string) int {
	switch in {
	case InPath:
		return 0
	case InQuery:
		return 1
	default:
		return 2
	}
}

func requestBody(ref *openapi3.RequestBodyRef) *Body {
	if ref == nil || ref.Value == nil {
		return nil
	}

	media := ref.Value.Content.Get(JSONContentType)
	contentType := JSONContentType
	if media == nil {
		contentType, media = firstContent(ref.Value.Content)
	}
	if media == nil {
		return nil
	}

	body := &Body{Required: ref.Value.Required, ContentType: contentType}
	if media.Schema == nil {
		return body
	}
	body.Schema = schemaName(media.Schema.Ref)
	if media.Schema.Value == nil {
		return body
	}

	schema := media.Schema.Value
	if !isObject(schema) || len(schema.Properties) == 0 {
		return body
	}

	required := map[string]bool{}
	for _, name := range schema.Required {
		required[name] = true
	}

	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	flat := true
	for _, name := range names {
		prop := schema.Properties[name]
		if prop == nil || prop.Value == nil {
			flat = false
			continue
		}
		t := schemaType(prop.Value)
		if t == TypeArray || isObject(prop.Value) {
			flat = false
		}
		body.Fields = append(body.Fields, Field{
			Name:        name,
			Type:        t,
			Required:    required[name],
			Description: prop.Value.Description,
			Enum:        enumStrings(prop.Value.Enum),
		})
	}
	body.Flat = flat
	return body
}

func firstContent(content openapi3.Content) (string, *openapi3.MediaType) {
	types := make([]string, 0, len(content))
	for t := range content {
		types = append(types, t)
	}
	sort.Strings(types)
	if len(types) == 0 {
		return "", nil
	}
	return types[0], content[types[0]]
}

func responseSchemas(responses *openapi3.Responses) map[string]*openapi3.SchemaRef {
	if responses == nil {
		return nil
	}
	out := map[string]*openapi3.SchemaRef{}
	for status, ref := range responses.Map() {
		if ref == nil || ref.Value == nil {
			continue
		}
		media := ref.Value.Content.Get(JSONContentType)
		if media == nil || media.Schema == nil {
			continue
		}
		out[status] = media.Schema
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func schemaType(s *openapi3.Schema) string {
	if s == nil || s.Type == nil {
		return TypeString
	}
	for _, t := range []string{TypeArray, TypeInteger, TypeNumber, TypeBoolean, TypeString, "object"} {
		if s.Type.Is(t) {
			return t
		}
	}
	return TypeString
}

func isObject(s *openapi3.Schema) bool {
	return s != nil && s.Type != nil && s.Type.Is("object")
}

func enumStrings(values []any) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, fmt.Sprint(v))
	}
	return out
}

func schemaName(ref string) string {
	if ref == "" {
		return ""
	}
	return path.Base(ref)
}
