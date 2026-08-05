package build

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"

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
	if version, ok := swaggerVersion(data); ok {
		return nil, output.Configf(
			"this document is Swagger %s, and blip only supports OpenAPI 3.x specs; "+
				"upgrade the spec or point blip at a different one", version)
	}

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false

	doc, err := loader.LoadFromData(data)
	if err != nil {
		return nil, output.Configf("parsing the spec: %w", err)
	}

	api := &API{}
	if doc.Info != nil {
		api.Title = doc.Info.Title
		api.Version = doc.Info.Version
	}
	if doc.Paths == nil {
		return api, nil
	}

	titleTag := assemblyTag(api.Title)
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

			group := groupFor(meaningfulTags(specOp.Tags, titleTag), p, renamedGroups, &api.Warnings)
			if groups[group] == nil {
				groups[group] = deduplicator{}
			}

			op := &Operation{
				ID:           specOp.OperationID,
				Group:        group,
				Method:       method,
				Path:         p,
				Summary:      specOp.Summary,
				Description:  specOp.Description,
				Deprecated:   specOp.Deprecated,
				Params:       params(item.Parameters, specOp.Parameters),
				Body:         requestBody(specOp.RequestBody),
				Source:       SourceSpec,
				responses:    responseSchemas(specOp.Responses),
				responseList: responseList(specOp.Responses),
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

// The loader below is OpenAPI-3-only; a 2.0 doc would parse silently into nothing.
func swaggerVersion(data []byte) (string, bool) {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return "", false
		}
	}
	v, ok := doc["swagger"]
	if !ok {
		return "", false
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	return fmt.Sprint(v), true
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

// assemblyTag is the tag .NET applies to endpoints declared without WithTags:
// the assembly name, which also opens the document title ("OrdersApi | v1").
func assemblyTag(title string) string {
	name, _, _ := strings.Cut(title, "|")
	return kebab(name)
}

// meaningfulTags drops a tag that only says which assembly served the endpoint.
func meaningfulTags(tags []string, titleTag string) []string {
	if titleTag == "" || len(tags) == 0 || kebab(tags[0]) != titleTag {
		return tags
	}
	return tags[1:]
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

// responseList resolves every documented response for display; unlike responseSchemas, it keeps statuses with no JSON body.
func responseList(responses *openapi3.Responses) []Response {
	if responses == nil {
		return nil
	}
	m := responses.Map()
	statuses := make([]string, 0, len(m))
	for status := range m {
		statuses = append(statuses, status)
	}
	// A plain sort already puts "default" last: ASCII digits precede letters.
	sort.Strings(statuses)

	out := make([]Response, 0, len(statuses))
	for _, status := range statuses {
		r := Response{Status: status}
		ref := m[status]
		if ref != nil && ref.Value != nil {
			if media := ref.Value.Content.Get(JSONContentType); media != nil && media.Schema != nil {
				r.Schema = schemaName(media.Schema.Ref)
				if media.Schema.Value != nil {
					s := media.Schema.Value
					r.Type = schemaType(s)
					if r.Type == TypeArray && s.Items != nil && s.Items.Value != nil {
						r.ItemType = schemaType(s.Items.Value)
					}
				}
			}
		}
		out = append(out, r)
	}
	return out
}

// typePrecedence decides which member of an OpenAPI 3.1 type union a flag binds
// to. "null" is not something anyone types at a prompt, and .NET pairs every
// numeric type with "string" so that query values may arrive string-encoded, so
// the specific type has to win over the permissive one.
var typePrecedence = []string{TypeArray, TypeObject, TypeInteger, TypeNumber, TypeBoolean, TypeString}

// schemaType reduces a schema to the one type blip can bind a flag to. It reads
// the type as a set rather than a single value, because 3.1 documents, which is
// what .NET 10 emits, routinely carry unions.
func schemaType(s *openapi3.Schema) string {
	if s == nil || s.Type == nil {
		return TypeString
	}

	present := make(map[string]bool, len(s.Type.Slice()))
	for _, t := range s.Type.Slice() {
		if t != "null" {
			present[t] = true
		}
	}
	for _, t := range typePrecedence {
		if present[t] {
			return t
		}
	}
	return TypeString
}

func isObject(s *openapi3.Schema) bool {
	return s != nil && s.Type != nil && schemaType(s) == TypeObject
}

// enumStrings drops a null member, which .NET emits for a nullable enum and
// which is not a value anyone can pass on a command line.
func enumStrings(values []any) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == nil {
			continue
		}
		out = append(out, fmt.Sprint(v))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func schemaName(ref string) string {
	if ref == "" {
		return ""
	}
	return path.Base(ref)
}
