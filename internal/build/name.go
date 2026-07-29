package build

import (
	"regexp"
	"strconv"
	"strings"
)

// Reserved names blip owns. A group that collides with one of these is renamed,
// because a spec must never be able to shadow blip's own commands.
var Reserved = map[string]bool{
	"version":  true,
	"envs":     true,
	"auth":     true,
	"raw":      true,
	"spec":     true,
	"describe": true,
	"call":     true,
	"help":     true,
	"blip":     true,
}

var (
	pathParam    = regexp.MustCompile(`\{([^}]+)\}`)
	nonAlnum     = regexp.MustCompile(`[^a-zA-Z0-9]+`)
	camelBoundry = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	acronymEnd   = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	dashRun      = regexp.MustCompile(`-+`)
)

// kebab turns listOrders, Products_GetAll or "Order Lines" into a lowercase
// dash-separated name.
func kebab(s string) string {
	s = acronymEnd.ReplaceAllString(s, "$1-$2")
	s = camelBoundry.ReplaceAllString(s, "$1-$2")
	s = nonAlnum.ReplaceAllString(s, "-")
	s = dashRun.ReplaceAllString(s, "-")
	return strings.Trim(strings.ToLower(s), "-")
}

func pathParamNames(path string) []string {
	matches := pathParam.FindAllStringSubmatch(path, -1)
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m[1])
	}
	return names
}

// pathSegments splits a path into its literal segments, dropping parameters and
// the noise that every .NET route carries.
func pathSegments(path string) []string {
	var segments []string
	for _, segment := range strings.Split(path, "/") {
		switch {
		case segment == "", strings.HasPrefix(segment, "{"):
			continue
		case isRoutePrefix(segment) && len(segments) == 0:
			continue
		}
		segments = append(segments, kebab(segment))
	}
	return segments
}

var versionSegment = regexp.MustCompile(`^v[0-9]+$`)

func isRoutePrefix(segment string) bool {
	lower := strings.ToLower(segment)
	return lower == "api" || versionSegment.MatchString(lower)
}

// deriveName builds a command name from method and path, for the minimal API
// endpoints that were declared without WithName and so have no operationId.
func deriveName(method, path string) string {
	parts := pathSegments(path)
	parts = append(parts, strings.ToLower(method))
	for _, param := range pathParamNames(path) {
		parts = append(parts, "by-"+kebab(param))
	}
	name := strings.Join(parts, "-")
	if name == "" {
		return strings.ToLower(method)
	}
	return name
}

// deriveGroup picks the parent command: the first tag when there is one,
// otherwise the first meaningful path segment.
func deriveGroup(tags []string, path string) string {
	if len(tags) > 0 && strings.TrimSpace(tags[0]) != "" {
		return kebab(tags[0])
	}
	if segments := pathSegments(path); len(segments) > 0 {
		return segments[0]
	}
	return "root"
}

// shortName strips the group from a full name, so that operationId listOrders in
// group orders becomes "list" rather than "list-orders".
func shortName(full, group string) string {
	if full == group {
		return full
	}
	parts := strings.Split(full, "-")
	groupParts := strings.Split(group, "-")

	if trimmed, ok := trimPrefix(parts, groupParts); ok && len(trimmed) > 0 {
		return strings.Join(trimmed, "-")
	}
	if trimmed, ok := trimSuffix(parts, groupParts); ok && len(trimmed) > 0 {
		return strings.Join(trimmed, "-")
	}

	// Singular against plural: listOrder in group orders.
	singular := singularize(group)
	if singular != group {
		if trimmed, ok := trimSuffix(parts, []string{singular}); ok && len(trimmed) > 0 {
			return strings.Join(trimmed, "-")
		}
		if trimmed, ok := trimPrefix(parts, []string{singular}); ok && len(trimmed) > 0 {
			return strings.Join(trimmed, "-")
		}
	}
	return full
}

func trimPrefix(parts, prefix []string) ([]string, bool) {
	if len(parts) < len(prefix) {
		return parts, false
	}
	for i, p := range prefix {
		if parts[i] != p {
			return parts, false
		}
	}
	return parts[len(prefix):], true
}

func trimSuffix(parts, suffix []string) ([]string, bool) {
	if len(parts) < len(suffix) {
		return parts, false
	}
	offset := len(parts) - len(suffix)
	for i, s := range suffix {
		if parts[offset+i] != s {
			return parts, false
		}
	}
	return parts[:offset], true
}

func singularize(s string) string {
	switch {
	case strings.HasSuffix(s, "ies"):
		return strings.TrimSuffix(s, "ies") + "y"
	case strings.HasSuffix(s, "ses"), strings.HasSuffix(s, "xes"):
		return strings.TrimSuffix(s, "es")
	case strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss"):
		return strings.TrimSuffix(s, "s")
	}
	return s
}

// deduplicate makes names unique within a namespace, appending a number. Callers
// must present names in a deterministic order or the numbering will move.
type deduplicator map[string]int

func (d deduplicator) unique(name string) string {
	count := d[name]
	d[name] = count + 1
	if count == 0 {
		return name
	}
	return name + "-" + strconv.Itoa(count+1)
}
