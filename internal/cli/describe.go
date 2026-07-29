package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

const describeLong = `List the whole API surface at once.

--compact prints one dense line per operation and nothing else. It exists so that
a caller can read the API once instead of running --help twenty times.

The notation is: path parameters bare and positional, query parameters as ?name,
header parameters as @name, a trailing * for required, and body:Schema for a
request body (body!:Schema when it is required).`

func newDescribeCommand(rt *Runtime) *cobra.Command {
	var compact, asJSON bool

	cmd := &cobra.Command{
		Use:   "describe",
		Short: "List every operation in one pass",
		Long:  describeLong,
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			api, err := rt.API(cmd.Context())
			if err != nil {
				return err
			}
			rt.ReportSpecWarnings(api, true)

			out := cmd.OutOrStdout()
			switch {
			case asJSON:
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(api.Listing())
			case compact:
				_, err := fmt.Fprintln(out, strings.Join(api.CompactLines(), "\n"))
				return err
			default:
				_, err := fmt.Fprintln(out, strings.Join(api.DescribeLines(), "\n"))
				return err
			}
		},
	}

	cmd.Flags().BoolVar(&compact, "compact", false, "one line per operation, no headings")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the same information as JSON")
	return cmd
}
