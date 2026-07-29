package cli

import (
	"github.com/spf13/cobra"

	"github.com/dotnetemmanuel/blip/internal/output"
)

func usageError(format string, args ...any) error {
	return output.Usagef(format, args...)
}

// usageArgs tags cobra's argument-count errors as usage errors, which is what
// they are; without this they would surface as an internal error.
func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		return output.WithCode(validate(cmd, args), output.ExitUsage)
	}
}
