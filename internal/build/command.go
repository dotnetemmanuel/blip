package build

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dotnetemmanuel/blip/internal/output"
)

// Invocation is an operation with its arguments bound, ready to be sent.
type Invocation struct {
	Operation *Operation
	Path      string
	Query     url.Values
	Header    http.Header
	Body      []byte
}

// Deps are what the command tree needs from its host: a way to send a request,
// and a way to turn --data and --field into a body.
type Deps struct {
	Send func(ctx context.Context, inv Invocation) error
	Body func(data string, fields []string) ([]byte, error)
}

func usageError(format string, args ...any) error {
	return output.WithCode(fmt.Errorf(format, args...), output.ExitUsage)
}

// Commands builds one cobra command per group, each with its operations beneath.
func (a *API) Commands(deps Deps) []*cobra.Command {
	var commands []*cobra.Command
	for _, op := range a.InGroup("") {
		commands = append(commands, operationCommand(op, op.Name, deps))
	}

	for _, group := range a.Groups() {
		if group == "" {
			continue
		}
		ops := a.InGroup(group)
		cmd := &cobra.Command{
			Use:   group,
			Short: groupSummary(group, ops),
			Args:  rejectUnknown("operation"),
			RunE: func(cmd *cobra.Command, _ []string) error {
				return cmd.Help()
			},
		}
		for _, op := range ops {
			cmd.AddCommand(operationCommand(op, op.Name, deps))
		}
		commands = append(commands, cmd)
	}
	return commands
}

// CallCommand addresses operations by their stable name, so a script keeps
// working even if grouping or a derived name changes.
func (a *API) CallCommand(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "call <operation> [args] [flags]",
		Short: "Call an operation by its operationId",
		Long: `Call an operation by its stable name.

The name is the operationId when the spec provides one, and group-name when it
does not. Unlike the generated tree, these names do not move when tags or
derivation change, so they are the safer thing to put in a script.`,
		Args: rejectUnknown("operation"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	for _, op := range a.Operations {
		cmd.AddCommand(operationCommand(op, op.FullName(), deps))
	}
	return cmd
}

// rejectUnknown turns a name that matched no subcommand into a usage error.
// Without it cobra would fall through to the parent's help and exit 0.
func rejectUnknown(kind string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		return usageError("unknown %s %q; run blip describe --compact to list them", kind, args[0])
	}
}

func groupSummary(group string, ops []*Operation) string {
	return fmt.Sprintf("%s (%d operations)", group, len(ops))
}

func operationCommand(op *Operation, name string, deps Deps) *cobra.Command {
	pathParams := op.PathParams()

	cmd := &cobra.Command{
		Use:          use(name, pathParams),
		Short:        short(op),
		Long:         long(op),
		SilenceUsage: true,
	}
	if op.Deprecated {
		cmd.Deprecated = "this operation is marked deprecated in the spec"
	}

	binder := newBinder(cmd.Flags(), op, deps)

	cmd.Args = func(_ *cobra.Command, args []string) error {
		if len(args) == len(pathParams) {
			return nil
		}
		if len(args) < len(pathParams) {
			return usageError("%s needs %s; missing %s",
				name, describeParams(pathParams), describeParams(pathParams[len(args):]))
		}
		return usageError("%s takes %d argument(s), got %d", name, len(pathParams), len(args))
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		inv, err := binder.bind(cmd.Flags(), args)
		if err != nil {
			return err
		}
		return deps.Send(cmd.Context(), *inv)
	}

	return cmd
}

func use(name string, pathParams []Param) string {
	parts := []string{name}
	for _, p := range pathParams {
		parts = append(parts, "<"+p.Name+">")
	}
	return strings.Join(parts, " ")
}

func short(op *Operation) string {
	if op.Summary != "" {
		return op.Summary
	}
	return op.Method + " " + op.Path
}

func long(op *Operation) string {
	var b strings.Builder
	if op.Summary != "" {
		b.WriteString(op.Summary + "\n\n")
	}
	if op.Description != "" && op.Description != op.Summary {
		b.WriteString(op.Description + "\n\n")
	}
	fmt.Fprintf(&b, "%s %s\n", op.Method, op.Path)
	if op.ID != "" {
		fmt.Fprintf(&b, "operationId: %s\n", op.ID)
	} else {
		b.WriteString("No operationId in the spec, so this name was derived from the method and path.\n")
	}

	if pathParams := op.PathParams(); len(pathParams) > 0 {
		b.WriteString("\nArguments:\n")
		for _, p := range pathParams {
			fmt.Fprintf(&b, "  %-16s %s\n", p.Name, strings.TrimSpace(p.Description+" "+typeNote(p)))
		}
	}
	if op.Body != nil {
		b.WriteString("\nBody:\n")
		fmt.Fprintf(&b, "  %s", bodyNote(op.Body))
	}
	return strings.TrimRight(b.String(), "\n")
}

func bodyNote(body *Body) string {
	name := body.Schema
	if name == "" {
		name = "request body"
	}
	required := "optional"
	if body.Required {
		required = "required"
	}
	if body.Flat {
		return fmt.Sprintf("%s (%s, %s). Use --data or --field.\n", name, body.ContentType, required)
	}
	return fmt.Sprintf("%s (%s, %s). Nested, so use --data.\n", name, body.ContentType, required)
}

func typeNote(p Param) string {
	note := "(" + p.Type
	if p.Type == TypeArray && p.ItemType != "" {
		note += " of " + p.ItemType
	}
	if len(p.Enum) > 0 {
		note += ": " + strings.Join(p.Enum, ", ")
	}
	note += ")"
	return note
}

func describeParams(params []Param) string {
	names := make([]string, 0, len(params))
	for _, p := range params {
		names = append(names, "<"+p.Name+">")
	}
	if len(names) == 0 {
		return "no arguments"
	}
	return strings.Join(names, " ")
}

// binder owns the flag names an operation registered, so that binding back to a
// request does not have to guess at them.
type binder struct {
	op      *Operation
	deps    Deps
	headers map[string]string
	data    string
	fields  []string

	queries    []string
	rawHeaders []string
}

func newBinder(flags *pflag.FlagSet, op *Operation, deps Deps) *binder {
	b := &binder{op: op, deps: deps, headers: map[string]string{}}

	for _, p := range op.ParamsIn(InQuery) {
		registerFlag(flags, p.Name, p, usageFor(p))
	}
	for _, p := range op.ParamsIn(InHeader) {
		flagName := "header-" + strings.ToLower(p.Name)
		b.headers[flagName] = p.Name
		registerFlag(flags, flagName, p, usageFor(p))
	}
	if op.Body != nil {
		flags.StringVar(&b.data, "data", "", "request body: inline, @file, or @- for stdin")
		if op.Body.Flat {
			flags.StringArrayVar(&b.fields, "field", nil, "body field key=value, or key:=value for raw JSON (repeatable)")
		}
	}

	// A declared route has no schema, so it gets the same open flags as raw.
	if op.Source == SourceRoute {
		flags.StringVar(&b.data, "data", "", "request body: inline, @file, or @- for stdin")
		flags.StringArrayVar(&b.fields, "field", nil, "body field key=value, or key:=value for raw JSON (repeatable)")
		flags.StringArrayVar(&b.queries, "query", nil, "query parameter name=value (repeatable)")
		flags.StringArrayVar(&b.rawHeaders, "header", nil, "request header \"Name: value\" (repeatable)")
	}
	return b
}

func usageFor(p Param) string {
	usage := p.Description
	if usage == "" {
		usage = p.In + " parameter"
	}
	if len(p.Enum) > 0 {
		usage += " (one of: " + strings.Join(p.Enum, ", ") + ")"
	}
	if p.Required {
		usage += " (required)"
	}
	return usage
}

func registerFlag(flags *pflag.FlagSet, name string, p Param, usage string) {
	switch p.Type {
	case TypeInteger:
		flags.Int64(name, defaultInt(p.Default), usage)
	case TypeNumber:
		flags.Float64(name, defaultFloat(p.Default), usage)
	case TypeBoolean:
		flags.Bool(name, p.Default == "true", usage)
	case TypeArray:
		flags.StringArray(name, nil, usage)
	default:
		flags.String(name, p.Default, usage)
	}
}

func defaultInt(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func defaultFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func (b *binder) bind(flags *pflag.FlagSet, args []string) (*Invocation, error) {
	inv := &Invocation{
		Operation: b.op,
		Query:     url.Values{},
		Header:    http.Header{},
	}

	pathParams := b.op.PathParams()
	path := b.op.Path
	for i, p := range pathParams {
		path = strings.Replace(path, "{"+p.Name+"}", url.PathEscape(args[i]), 1)
	}
	inv.Path = path

	var missing []string
	for _, p := range b.op.ParamsIn(InQuery) {
		values, err := flagValues(flags, p.Name, p)
		if err != nil {
			return nil, err
		}
		if len(values) == 0 {
			if p.Required {
				missing = append(missing, "--"+p.Name)
			}
			continue
		}
		for _, v := range values {
			inv.Query.Add(p.Name, v)
		}
	}

	for flagName, headerName := range b.headers {
		p := paramByName(b.op.ParamsIn(InHeader), headerName)
		values, err := flagValues(flags, flagName, p)
		if err != nil {
			return nil, err
		}
		if len(values) == 0 {
			if p.Required {
				missing = append(missing, "--"+flagName)
			}
			continue
		}
		for _, v := range values {
			inv.Header.Add(headerName, v)
		}
	}

	if len(missing) > 0 {
		return nil, usageError("%s is missing required parameters: %s", b.op.FullName(), strings.Join(missing, ", "))
	}

	if b.op.Source == SourceRoute {
		if err := applyPairs(inv, b.queries, b.rawHeaders); err != nil {
			return nil, err
		}
		body, err := b.deps.Body(b.data, b.fields)
		if err != nil {
			return nil, err
		}
		inv.Body = body
		return inv, nil
	}

	if b.op.Body != nil {
		body, err := b.deps.Body(b.data, b.fields)
		if err != nil {
			return nil, err
		}
		if len(body) == 0 && b.op.Body.Required {
			return nil, usageError("%s requires a body: pass --data @file.json, --data @- or %s",
				b.op.FullName(), fieldHint(b.op.Body))
		}
		inv.Body = body
		if b.op.Body.ContentType != "" && len(body) > 0 {
			inv.Header.Set("Content-Type", b.op.Body.ContentType)
		}
	}

	return inv, nil
}

func fieldHint(body *Body) string {
	if !body.Flat {
		return "inline JSON"
	}
	for _, f := range body.Fields {
		if f.Required {
			return "--field " + f.Name + "=..."
		}
	}
	return "--field key=value"
}

func paramByName(params []Param, name string) Param {
	for _, p := range params {
		if p.Name == name {
			return p
		}
	}
	return Param{Name: name, Type: TypeString}
}

// flagValues reads a flag back as strings. An unset flag yields nothing, so a
// default in the spec is never sent as though the caller had asked for it.
func flagValues(flags *pflag.FlagSet, name string, p Param) ([]string, error) {
	if !flags.Changed(name) {
		return nil, nil
	}

	switch p.Type {
	case TypeInteger:
		v, err := flags.GetInt64(name)
		if err != nil {
			return nil, usageError("--%s: %v", name, err)
		}
		return []string{strconv.FormatInt(v, 10)}, nil
	case TypeNumber:
		v, err := flags.GetFloat64(name)
		if err != nil {
			return nil, usageError("--%s: %v", name, err)
		}
		return []string{strconv.FormatFloat(v, 'g', -1, 64)}, nil
	case TypeBoolean:
		v, err := flags.GetBool(name)
		if err != nil {
			return nil, usageError("--%s: %v", name, err)
		}
		return []string{strconv.FormatBool(v)}, nil
	case TypeArray:
		v, err := flags.GetStringArray(name)
		if err != nil {
			return nil, usageError("--%s: %v", name, err)
		}
		return v, nil
	default:
		v, err := flags.GetString(name)
		if err != nil {
			return nil, usageError("--%s: %v", name, err)
		}
		if len(p.Enum) > 0 && !contains(p.Enum, v) {
			return nil, usageError("--%s=%q is not one of: %s", name, v, strings.Join(p.Enum, ", "))
		}
		return []string{v}, nil
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// applyPairs binds the open --query and --header flags a declared route carries.
func applyPairs(inv *Invocation, queries, headers []string) error {
	for _, raw := range queries {
		name, value, found := strings.Cut(raw, "=")
		if !found || name == "" {
			return usageError("--query %q must be name=value", raw)
		}
		inv.Query.Add(name, value)
	}
	for _, raw := range headers {
		name, value, found := strings.Cut(raw, ":")
		if !found {
			name, value, found = strings.Cut(raw, "=")
		}
		name = strings.TrimSpace(name)
		if !found || name == "" {
			return usageError("--header %q must be \"Name: value\"", raw)
		}
		inv.Header.Add(name, strings.TrimSpace(value))
	}
	return nil
}
