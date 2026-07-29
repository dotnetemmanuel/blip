package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dotnetemmanuel/blip/internal/build"
	"github.com/dotnetemmanuel/blip/internal/request"
)

// Flag names blip owns on every operation, over and above the globals. A spec
// parameter that wants one of these is renamed rather than allowed to take it.
var ownedFlags = []string{"data", "field", "query", "header"}

// operationCommands builds the group commands for an API, plus any top-level
// commands declared routes contributed.
func operationCommands(rt *Runtime, api *build.API, globals *pflag.FlagSet) []*cobra.Command {
	var commands []*cobra.Command

	for _, op := range api.InGroup("") {
		commands = append(commands, newOperationCommand(rt, op, op.Name, globals))
	}

	for _, group := range api.Groups() {
		if group == "" {
			continue
		}
		ops := api.InGroup(group)
		cmd := &cobra.Command{
			Use:   group,
			Short: fmt.Sprintf("%s (%d operations)", group, len(ops)),
			Args:  rejectUnknown("operation"),
			RunE: func(cmd *cobra.Command, _ []string) error {
				return cmd.Help()
			},
		}
		for _, op := range ops {
			cmd.AddCommand(newOperationCommand(rt, op, op.Name, globals))
		}
		commands = append(commands, cmd)
	}
	return commands
}

// newCallCommand addresses operations by their stable name, so a script keeps
// working even if grouping or a derived name changes.
func newCallCommand(rt *Runtime, api *build.API, globals *pflag.FlagSet) *cobra.Command {
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
	for _, op := range api.Operations {
		cmd.AddCommand(newOperationCommand(rt, op, op.FullName(), globals))
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

func newOperationCommand(rt *Runtime, op *build.Operation, name string, globals *pflag.FlagSet) *cobra.Command {
	pathParams := op.PathParams()

	cmd := &cobra.Command{
		Use:          use(name, pathParams),
		Short:        short(op),
		SilenceUsage: true,
	}
	if op.Deprecated {
		cmd.Deprecated = "this operation is marked deprecated in the spec"
	}

	binder := newBinder(cmd.Flags(), globals, op)
	cmd.Long = long(op, binder.renamed)

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
		req, err := binder.bind(rt, cmd.Flags(), args)
		if err != nil {
			return err
		}
		return rt.send(cmd.Context(), req, op)
	}

	return cmd
}

// binder owns the flag names an operation registered, so that binding back to a
// request never has to guess at them.
type binder struct {
	op *build.Operation

	// flagFor maps a parameter to the flag it actually got, which is not its own
	// name when the spec asked for one blip already owns.
	flagFor map[string]string
	renamed map[string]string

	data       string
	fields     []string
	queries    []string
	rawHeaders []string
}

func newBinder(flags, globals *pflag.FlagSet, op *build.Operation) *binder {
	b := &binder{
		op:      op,
		flagFor: map[string]string{},
		renamed: map[string]string{},
	}

	// blip's own flags are claimed first, so a parameter is what moves aside.
	claimed := map[string]bool{}
	for _, name := range ownedFlags {
		claimed[name] = true
	}

	if op.Body != nil || op.Source == build.SourceRoute {
		flags.StringVar(&b.data, "data", "", "request body: inline, @file, or @- for stdin")
	}
	if (op.Body != nil && op.Body.Flat) || op.Source == build.SourceRoute {
		flags.StringArrayVar(&b.fields, "field", nil, "body field key=value, or key:=value for raw JSON (repeatable)")
	}
	// A declared route has no schema, so it gets the same open flags as raw.
	if op.Source == build.SourceRoute {
		flags.StringArrayVar(&b.queries, "query", nil, "query parameter name=value (repeatable)")
		flags.StringArrayVar(&b.rawHeaders, "header", nil, "request header \"Name: value\" (repeatable)")
	}

	for _, p := range op.ParamsIn(build.InQuery) {
		b.register(flags, globals, claimed, p, p.Name)
	}
	for _, p := range op.ParamsIn(build.InHeader) {
		b.register(flags, globals, claimed, p, "header-"+strings.ToLower(p.Name))
	}

	return b
}

// register gives a parameter a flag, moving it out of the way of blip's own
// flags. Letting a spec claim --dry-run or --output would let it turn off the
// safety net or silently change what lands on stdout.
func (b *binder) register(flags, globals *pflag.FlagSet, claimed map[string]bool, p build.Param, preferred string) {
	taken := func(name string) bool {
		return claimed[name] || flags.Lookup(name) != nil || (globals != nil && globals.Lookup(name) != nil)
	}

	name := preferred
	if taken(name) && !strings.HasPrefix(preferred, p.In+"-") {
		name = p.In + "-" + preferred
	}
	for suffix := 2; taken(name); suffix++ {
		name = fmt.Sprintf("%s-%d", preferred, suffix)
	}

	claimed[name] = true
	b.flagFor[p.In+":"+p.Name] = name
	if name != preferred {
		b.renamed[preferred] = name
	}
	registerFlag(flags, name, p, usageFor(p, name, preferred))
}

func (b *binder) flag(p build.Param) string {
	if name, ok := b.flagFor[p.In+":"+p.Name]; ok {
		return name
	}
	return p.Name
}

func usageFor(p build.Param, flagName, preferred string) string {
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
	if flagName != preferred {
		usage += fmt.Sprintf(" (sends %s=; renamed, --%s belongs to blip)", p.Name, preferred)
	}
	return usage
}

func registerFlag(flags *pflag.FlagSet, name string, p build.Param, usage string) {
	switch p.Type {
	case build.TypeInteger:
		flags.Int64(name, defaultInt(p.Default), usage)
	case build.TypeNumber:
		flags.Float64(name, defaultFloat(p.Default), usage)
	case build.TypeBoolean:
		flags.Bool(name, p.Default == "true", usage)
	case build.TypeArray:
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

func (b *binder) bind(rt *Runtime, flags *pflag.FlagSet, args []string) (*request.Request, error) {
	req := &request.Request{
		Method: b.op.Method,
		Query:  url.Values{},
		Header: http.Header{},
	}

	path := b.op.Path
	for i, p := range b.op.PathParams() {
		path = strings.Replace(path, "{"+p.Name+"}", url.PathEscape(args[i]), 1)
	}
	req.Path = path

	var missing []string
	// Both loops walk the parameters in spec order, never a map, so the message
	// a caller sees is the same on every run.
	for _, p := range b.op.ParamsIn(build.InQuery) {
		values, err := flagValues(flags, b.flag(p), p)
		if err != nil {
			return nil, err
		}
		if len(values) == 0 {
			if p.Required {
				missing = append(missing, "--"+b.flag(p))
			}
			continue
		}
		for _, v := range values {
			req.Query.Add(p.Name, v)
		}
	}

	for _, p := range b.op.ParamsIn(build.InHeader) {
		values, err := flagValues(flags, b.flag(p), p)
		if err != nil {
			return nil, err
		}
		if len(values) == 0 {
			if p.Required {
				missing = append(missing, "--"+b.flag(p))
			}
			continue
		}
		for _, v := range values {
			req.Header.Add(p.Name, v)
		}
	}

	if len(missing) > 0 {
		return nil, usageError("%s is missing required parameters: %s",
			b.op.FullName(), strings.Join(missing, ", "))
	}

	if b.op.Source == build.SourceRoute {
		if err := b.applyOpenFlags(req); err != nil {
			return nil, err
		}
		body, err := rt.body(b.data, b.fields)
		if err != nil {
			return nil, err
		}
		req.Body = body
		return req, nil
	}

	if b.op.Body != nil {
		body, err := rt.body(b.data, b.fields)
		if err != nil {
			return nil, err
		}
		if len(body) == 0 && b.op.Body.Required {
			return nil, usageError("%s requires a body: pass --data @file.json, --data @- or %s",
				b.op.FullName(), fieldHint(b.op.Body))
		}
		req.Body = body
		if b.op.Body.ContentType != "" && len(body) > 0 {
			req.Header.Set("Content-Type", b.op.Body.ContentType)
		}
	}

	return req, nil
}

// applyOpenFlags binds the --query and --header flags a declared route carries,
// using the same parsing raw uses so the two cannot drift apart.
func (b *binder) applyOpenFlags(req *request.Request) error {
	query, err := request.ParseQuery(b.queries)
	if err != nil {
		return err
	}
	for name, values := range query {
		for _, v := range values {
			req.Query.Add(name, v)
		}
	}

	header, err := request.ParseHeaders(b.rawHeaders)
	if err != nil {
		return err
	}
	for name, values := range header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	return nil
}

func fieldHint(body *build.Body) string {
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

// flagValues reads a flag back as strings. An unset flag yields nothing, so a
// default in the spec is never sent as though the caller had asked for it.
func flagValues(flags *pflag.FlagSet, name string, p build.Param) ([]string, error) {
	if !flags.Changed(name) {
		return nil, nil
	}

	switch p.Type {
	case build.TypeInteger:
		v, err := flags.GetInt64(name)
		if err != nil {
			return nil, usageError("--%s: %v", name, err)
		}
		return []string{strconv.FormatInt(v, 10)}, nil
	case build.TypeNumber:
		v, err := flags.GetFloat64(name)
		if err != nil {
			return nil, usageError("--%s: %v", name, err)
		}
		return []string{strconv.FormatFloat(v, 'g', -1, 64)}, nil
	case build.TypeBoolean:
		v, err := flags.GetBool(name)
		if err != nil {
			return nil, usageError("--%s: %v", name, err)
		}
		return []string{strconv.FormatBool(v)}, nil
	case build.TypeArray:
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

func use(name string, pathParams []build.Param) string {
	parts := []string{name}
	for _, p := range pathParams {
		parts = append(parts, "<"+p.Name+">")
	}
	return strings.Join(parts, " ")
}

func short(op *build.Operation) string {
	if op.Summary != "" {
		return op.Summary
	}
	return op.Method + " " + op.Path
}

func long(op *build.Operation, renamed map[string]string) string {
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
	if len(renamed) > 0 {
		b.WriteString("\nRenamed flags, because blip already owns those names:\n")
		for _, from := range sortedKeys(renamed) {
			fmt.Fprintf(&b, "  --%s is --%s\n", from, renamed[from])
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func bodyNote(body *build.Body) string {
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

func typeNote(p build.Param) string {
	note := "(" + p.Type
	if p.Type == build.TypeArray && p.ItemType != "" {
		note += " of " + p.ItemType
	}
	if len(p.Enum) > 0 {
		note += ": " + strings.Join(p.Enum, ", ")
	}
	note += ")"
	return note
}

func describeParams(params []build.Param) string {
	names := make([]string, 0, len(params))
	for _, p := range params {
		names = append(names, "<"+p.Name+">")
	}
	if len(names) == 0 {
		return "no arguments"
	}
	return strings.Join(names, " ")
}
