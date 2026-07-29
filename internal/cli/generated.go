package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dotnetemmanuel/blip/internal/build"
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

	deps := build.Deps{
		Send: func(ctx context.Context, inv build.Invocation) error {
			return rt.send(ctx, call{
				Method:    inv.Operation.Method,
				Path:      inv.Path,
				Query:     inv.Query,
				Header:    inv.Header,
				Body:      inv.Body,
				operation: inv.Operation,
			})
		},
		Body: rt.body,
	}

	root.AddCommand(api.Commands(deps)...)
	root.AddCommand(api.CallCommand(deps))
	return nil
}
