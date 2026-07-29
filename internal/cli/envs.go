package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type envListing struct {
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	SpecURL  string `json:"spec_url,omitempty"`
	Auth     string `json:"auth,omitempty"`
	Timeout  string `json:"timeout"`
	Readonly bool   `json:"readonly"`
	Insecure bool   `json:"insecure"`
	MTLS     bool   `json:"mtls"`
	Default  bool   `json:"default"`
	Selected bool   `json:"selected"`
}

func newEnvsCommand(rt *Runtime) *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "envs",
		Short: "List environments and their resolved base URLs",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			listings, err := collectEnvs(rt)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(listings)
			}
			return writeEnvTable(cmd, listings)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

func collectEnvs(rt *Runtime) ([]envListing, error) {
	cfg, err := rt.Config()
	if err != nil {
		return nil, err
	}

	// A bad --env should not stop envs from listing what is available; that listing
	// is exactly what the user needs in order to fix it.
	selected := ""
	if env, err := rt.Env(); err == nil {
		selected = env.Name
	}

	names := cfg.EnvNames()
	listings := make([]envListing, 0, len(names))
	for _, name := range names {
		env, err := cfg.ResolveEnv(name)
		if err != nil {
			return nil, err
		}
		listings = append(listings, envListing{
			Name:     name,
			BaseURL:  env.BaseURL.String(),
			SpecURL:  env.SpecURL,
			Auth:     env.Auth,
			Timeout:  env.Timeout.String(),
			Readonly: env.Readonly,
			Insecure: env.Insecure,
			MTLS:     env.ClientCert != "",
			Default:  name == cfg.DefaultEnv,
			Selected: name == selected,
		})
	}
	return listings, nil
}

func writeEnvTable(cmd *cobra.Command, listings []envListing) error {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	for _, e := range listings {
		marker := " "
		if e.Selected {
			marker = "*"
		}
		auth := e.Auth
		if auth == "" {
			auth = "none"
		}
		fmt.Fprintf(w, "%s %s\t%s\t%s\t%s\n", marker, e.Name, e.BaseURL, auth, strings.Join(envTags(e), " "))
	}
	return w.Flush()
}

func envTags(e envListing) []string {
	var tags []string
	if e.Readonly {
		tags = append(tags, "readonly")
	}
	if e.Insecure {
		tags = append(tags, "insecure")
	}
	if e.MTLS {
		tags = append(tags, "mtls")
	}
	if e.Default {
		tags = append(tags, "default")
	}
	return tags
}
