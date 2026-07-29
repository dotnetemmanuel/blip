// Package cli assembles blip's command tree and maps failures onto exit codes.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/dotnetemmanuel/blip/internal/output"
)

const rootLong = `blip turns an HTTP API with an OpenAPI spec into a CLI.

Configuration lives in .blip.toml at your repo root and is committed.
Credentials live in ~/.config/blip/credentials.toml and are not.

Output is machine-parseable by default: the response body on stdout,
every diagnostic on stderr, and an exit code you can branch on.`

// NewRootCommand builds a command tree bound to rt. Nothing is package-global, so
// tests can run commands independently.
func NewRootCommand(rt *Runtime) *cobra.Command {
	root := &cobra.Command{
		Use:               "blip",
		Short:             "Call an HTTP API from its OpenAPI spec",
		Long:              rootLong,
		SilenceUsage:      true,
		SilenceErrors:     true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}

	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return output.WithCode(err, output.ExitUsage)
	})

	rt.Globals.register(root.PersistentFlags())

	root.AddCommand(
		newVersionCommand(rt),
		newEnvsCommand(rt),
		newAuthCommand(rt),
		newRawCommand(rt),
		newSpecCommand(rt),
		newDescribeCommand(rt),
		newInitCommand(rt),
	)

	return root
}

// Execute runs the command tree and returns the process exit code.
func Execute(args []string, stdout, stderr io.Writer) int {
	rt := &Runtime{
		Globals:     &Globals{},
		Stdout:      stdout,
		Stderr:      stderr,
		Stdin:       os.Stdin,
		StdinIsTTY:  isTerminal(os.Stdin),
		StdoutIsTTY: isTerminalWriter(stdout),
	}
	rt.Globals.prescan(args)
	return Run(rt, args)
}

// Run executes args against an already-built runtime and returns the exit code.
func Run(rt *Runtime, args []string) int {
	root := NewRootCommand(rt)

	if wantsSpec(args) {
		if err := attachGenerated(context.Background(), rt, root); err != nil {
			if !isHelpOnly(args) {
				fmt.Fprintf(rt.Stderr, "blip: %s\n", err)
				return output.ExitCodeFor(classify(err))
			}
			rt.Warnf("%v", err)
		}
	}

	root.SetOut(rt.Stdout)
	root.SetErr(rt.Stderr)
	root.SetArgs(args)

	err := root.ExecuteContext(context.Background())
	if err == nil {
		return output.ExitOK
	}

	if msg := err.Error(); msg != "" {
		fmt.Fprintf(rt.Stderr, "blip: %s\n", rt.Redact(msg))
	}
	return output.ExitCodeFor(classify(err))
}

// cobra reports unknown commands and surplus arguments as untyped errors, so the
// message text is the only thing left to key on.
func classify(err error) error {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return err
	}
	if strings.HasPrefix(err.Error(), "unknown command") {
		return output.WithCode(err, output.ExitUsage)
	}
	return err
}

func isTerminal(f *os.File) bool {
	return f != nil && term.IsTerminal(int(f.Fd()))
}

func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isTerminal(f)
}
