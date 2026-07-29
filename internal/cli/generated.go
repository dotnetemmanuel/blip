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
	"envs":       true,
	"auth":       true,
	"raw":        true,
	"spec":       true,
	"help":       true,
	"completion": true,
}

// wantsSpec reports whether the invocation needs the generated tree. Reading it
// from argv is unavoidable: the tree has to exist before cobra can parse against it.
func wantsSpec(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return true
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return !specless[arg]
	}
	// Bare blip, or flags only: build the tree so that --help lists the API.
	return true
}

// isHelpOnly reports whether the invocation only wants help, in which case a
// spec that will not load is a warning rather than a failure.
func isHelpOnly(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" || arg == "help" {
			return true
		}
		if !strings.HasPrefix(arg, "-") {
			return false
		}
	}
	return true
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
