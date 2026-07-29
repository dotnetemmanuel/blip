package build

import (
	"fmt"
	"strings"

	"github.com/dotnetemmanuel/blip/internal/output"
)

// Route is a hand-declared endpoint from .blip.toml, for APIs with no spec or
// endpoints a spec forgot.
type Route struct {
	Name    string
	Method  string
	Path    string
	Summary string
	Group   string
}

// AddRoutes merges declared routes into the tree. A route with no group becomes
// a top-level command, which is what a config that names one is asking for.
func (a *API) AddRoutes(routes []Route) error {
	for _, r := range routes {
		name := kebab(r.Name)
		group := kebab(r.Group)

		if group == "" && Reserved[name] {
			return output.WithCode(fmt.Errorf(
				"route %q collides with a blip command; rename it or give it a group", r.Name), output.ExitConfig)
		}
		if group != "" && Reserved[group] {
			return output.WithCode(fmt.Errorf(
				"route group %q collides with a blip command; rename it", r.Group), output.ExitConfig)
		}
		if existing := a.findExact(group, name); existing != nil {
			return output.WithCode(fmt.Errorf(
				"route %q collides with the generated command for %s %s; rename it or give it a group",
				r.Name, existing.Method, existing.Path), output.ExitConfig)
		}

		a.Operations = append(a.Operations, &Operation{
			Group:   group,
			Name:    name,
			Method:  strings.ToUpper(r.Method),
			Path:    r.Path,
			Summary: r.Summary,
			Params:  routeParams(r.Path),
			Source:  SourceRoute,
		})
	}

	sortOperations(a.Operations)
	return nil
}

func (a *API) findExact(group, name string) *Operation {
	for _, op := range a.Operations {
		if op.Group == group && op.Name == name {
			return op
		}
	}
	return nil
}

// routeParams reads path parameters straight out of the declared path, since a
// route has no schema to consult.
func routeParams(path string) []Param {
	names := pathParamNames(path)
	params := make([]Param, 0, len(names))
	for _, name := range names {
		params = append(params, Param{Name: name, In: InPath, Required: true, Type: TypeString})
	}
	return params
}
