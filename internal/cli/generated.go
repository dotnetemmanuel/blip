package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
)

// specless are the commands that must work with no spec at all, and so must
// never pay for fetching one.
var specless = map[string]bool{
	"version":    true,
	"init":       true,
	"envs":       true,
	"auth":       true,
	"raw":        true,
	"spec":       true,
	"help":       true,
	"completion": true,
}

// valueFlags are the globals written as two tokens, whose value must not be
// mistaken for the subcommand.
var valueFlags = map[string]bool{
	"--config": true, "--env": true, "--profile": true, "--timeout": true, "--output": true,
}

// subcommand finds the first real command word in argv, stepping over flags and
// the values they consume. Reading it from argv is unavoidable: the tree has to
// exist before cobra can parse against it.
func subcommand(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			return "", false
		case valueFlags[arg]:
			i++
		case strings.HasPrefix(arg, "-"):
		default:
			return arg, true
		}
	}
	return "", false
}

// wantsSpec reports whether the invocation needs the generated tree.
func wantsSpec(args []string) bool {
	name, ok := subcommand(args)
	if !ok {
		// Bare blip, or flags only: build the tree so that --help lists the API.
		return true
	}
	return !specless[name]
}

// isHelpOnly reports whether the invocation only wants help, in which case a
// spec that will not load is a warning rather than a failure.
func isHelpOnly(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--help" || arg == "-h" || arg == "help" {
			return true
		}
	}
	_, ok := subcommand(args)
	return !ok
}

// attachGenerated adds the spec-derived commands, plus any [[route]] entries,
// to the root command.
func attachGenerated(ctx context.Context, rt *Runtime, root *cobra.Command) error {
	api, err := rt.API(ctx)
	if err != nil {
		return err
	}

	globals := root.PersistentFlags()
	root.AddCommand(operationCommands(rt, api, globals)...)
	root.AddCommand(newCallCommand(rt, api, globals))
	return nil
}
