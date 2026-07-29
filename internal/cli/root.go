// Package cli assembles blip's command tree and maps failures onto exit codes.
package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dotnetemmanuel/blip/internal/output"
)

const rootLong = `blip turns an HTTP API with an OpenAPI spec into a CLI.

Configuration lives in .blip.toml at your repo root and is committed.
Credentials live in ~/.config/blip/credentials.toml and are not.

Output is machine-parseable by default: the response body on stdout,
every diagnostic on stderr, and an exit code you can branch on.`

// NewRootCommand builds a fresh command tree. Nothing is package-global, so tests
// can run commands independently.
func NewRootCommand() *cobra.Command {
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

	root.AddCommand(newVersionCommand())

	return root
}

// Execute runs the command tree and returns the process exit code.
func Execute(args []string, stdout, stderr io.Writer) int {
	root := NewRootCommand()
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(args)

	err := root.Execute()
	if err == nil {
		return output.ExitOK
	}

	fmt.Fprintf(stderr, "blip: %v\n", err)
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
