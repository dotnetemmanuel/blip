package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newAuthCommand(rt *Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Inspect credential resolution",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newAuthTestCommand(rt))
	return cmd
}

func newAuthTestCommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "test",
		Short: "Resolve the credentials for an environment without revealing them",
		Long: `Resolve the credentials for the selected environment.

Any _command is executed and, for oauth2_cc, a token is actually fetched, so a
success here means the credential really can be produced. Secrets are reported
only as a fingerprint.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := rt.Env()
			if err != nil {
				return err
			}
			profile, err := rt.ProfileName()
			if err != nil {
				return err
			}
			if profile == "" {
				profile = "(none configured)"
			}

			a, err := rt.Authenticator(cmd.Context())
			if err != nil {
				return err
			}
			detail, err := a.Describe(cmd.Context())
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "env\t%s\n", env.Name)
			fmt.Fprintf(w, "base_url\t%s\n", env.BaseURL)
			fmt.Fprintf(w, "profile\t%s\n", profile)
			fmt.Fprintf(w, "resolved\t%s\n", detail)
			return w.Flush()
		},
	}
}
