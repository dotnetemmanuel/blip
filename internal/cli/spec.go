package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newSpecCommand(rt *Runtime) *cobra.Command {
	var showPath, showMeta bool

	cmd := &cobra.Command{
		Use:   "spec",
		Short: "Show or refresh the cached OpenAPI spec",
		Long: `Print the spec blip is working from.

The cache is revalidated with If-None-Match on every run, so a spec that has not
changed costs one small conditional request. --refresh forces a full fetch,
--offline forbids the network entirely, and if the backend cannot be reached the
cached copy is used with a warning.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := rt.Spec(cmd.Context())
			if err != nil {
				return err
			}

			switch {
			case showPath:
				_, err := fmt.Fprintln(cmd.OutOrStdout(), s.Path)
				return err
			case showMeta:
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintf(w, "url\t%s\n", s.Meta.URL)
				fmt.Fprintf(w, "etag\t%s\n", orNone(s.Meta.ETag))
				fmt.Fprintf(w, "fetched\t%s\n", s.Meta.FetchedAt.Format("2006-01-02T15:04:05Z"))
				fmt.Fprintf(w, "status\t%s\n", s.Status)
				fmt.Fprintf(w, "path\t%s\n", s.Path)
				fmt.Fprintf(w, "bytes\t%d\n", len(s.Data))
				return w.Flush()
			default:
				_, err := cmd.OutOrStdout().Write(s.Data)
				return err
			}
		},
	}

	cmd.Flags().BoolVar(&showPath, "path", false, "print the cache file path instead of the spec")
	cmd.Flags().BoolVar(&showMeta, "meta", false, "print where the spec came from and when")
	return cmd
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
