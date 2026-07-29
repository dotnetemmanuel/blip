package build

import (
	"fmt"
	"strings"
)

// CompactLines renders one dense line per operation. This is a contract an agent
// reads once at the start of a session, so the shape must stay stable:
//
//	<group> <name>  <METHOD>  <path>  <params>
//
// Path parameters appear bare, query parameters as ?name, headers as @name, a
// required parameter carries a trailing *, and a body as body:Schema or
// body!:Schema when required.
func (a *API) CompactLines() []string {
	if len(a.Operations) == 0 {
		return nil
	}

	names := make([]string, len(a.Operations))
	for i, op := range a.Operations {
		names[i] = commandPath(op)
	}
	nameWidth := widest(names)
	pathWidth := widest(paths(a.Operations))

	lines := make([]string, 0, len(a.Operations))
	for i, op := range a.Operations {
		line := fmt.Sprintf("%-*s  %-6s  %-*s", nameWidth, names[i], op.Method, pathWidth, op.Path)
		if notes := paramNotes(op); notes != "" {
			line += "  " + notes
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	return lines
}

func commandPath(op *Operation) string {
	if op.Group == "" {
		return op.Name
	}
	return op.Group + " " + op.Name
}

func paths(ops []*Operation) []string {
	out := make([]string, len(ops))
	for i, op := range ops {
		out[i] = op.Path
	}
	return out
}

func widest(values []string) int {
	width := 0
	for _, v := range values {
		if len(v) > width {
			width = len(v)
		}
	}
	return width
}

func paramNotes(op *Operation) string {
	var notes []string

	for _, p := range op.PathParams() {
		notes = append(notes, p.Name)
	}
	for _, p := range op.ParamsIn(InQuery) {
		notes = append(notes, "?"+p.Name+requiredMark(p.Required))
	}
	for _, p := range op.ParamsIn(InHeader) {
		notes = append(notes, "@"+p.Name+requiredMark(p.Required))
	}

	parts := []string{}
	if joined := strings.Join(notes, ","); joined != "" {
		parts = append(parts, joined)
	}
	if op.Body != nil {
		schema := op.Body.Schema
		if schema == "" {
			schema = "json"
		}
		marker := "body:"
		if op.Body.Required {
			marker = "body!:"
		}
		parts = append(parts, marker+schema)
	}
	if op.Source == SourceRoute {
		parts = append(parts, "(route)")
	}
	return strings.Join(parts, "  ")
}

func requiredMark(required bool) string {
	if required {
		return "*"
	}
	return ""
}

// Legend explains the compact notation. It is printed by describe, but never by
// describe --compact, which stays pure data.
const Legend = "name  METHOD  path  args: bare=positional, ?query, @header, *=required, body:Schema"

// DescribeLines renders the readable listing: grouped, with summaries.
func (a *API) DescribeLines() []string {
	lines := []string{
		fmt.Sprintf("%s %s", a.Title, a.Version),
		Legend,
	}

	compact := a.CompactLines()
	byIndex := map[*Operation]string{}
	for i, op := range a.Operations {
		byIndex[op] = compact[i]
	}

	for _, group := range a.Groups() {
		label := group
		if label == "" {
			label = "(top level)"
		}
		lines = append(lines, "", label)
		for _, op := range a.InGroup(group) {
			line := "  " + byIndex[op]
			if op.Summary != "" {
				line += "  # " + op.Summary
			}
			lines = append(lines, line)
		}
	}
	return lines
}

// Listing is the JSON shape of describe --json, which adds the stable name that
// the model itself only computes.
type Listing struct {
	Title      string            `json:"title"`
	Version    string            `json:"version"`
	Operations []ListedOperation `json:"operations"`
}

// ListedOperation is one operation as described.
type ListedOperation struct {
	*Operation
	FullName string `json:"full_name"`
	Command  string `json:"command"`
}

// Listing returns the structured description.
func (a *API) Listing() Listing {
	ops := make([]ListedOperation, 0, len(a.Operations))
	for _, op := range a.Operations {
		ops = append(ops, ListedOperation{
			Operation: op,
			FullName:  op.FullName(),
			Command:   commandPath(op),
		})
	}
	return Listing{Title: a.Title, Version: a.Version, Operations: ops}
}
