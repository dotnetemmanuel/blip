package cli

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Set with -ldflags at release time; otherwise blip falls back to build info.
var (
	version string
	commit  string
	date    string
)

const unknown = "unknown"

// Version reports the version blip should call itself.
func Version() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "0.0.0-dev"
}

func commitOrBuildInfo() string {
	if commit != "" {
		return commit
	}
	return buildSetting("vcs.revision")
}

func dateOrBuildInfo() string {
	if date != "" {
		return date
	}
	return buildSetting("vcs.time")
}

func buildSetting(key string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return unknown
	}
	for _, s := range info.Settings {
		if s.Key == key && s.Value != "" {
			return s.Value
		}
	}
	return unknown
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the blip version",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "blip %s (commit %s, built %s, %s %s/%s)\n",
				Version(),
				commitOrBuildInfo(),
				dateOrBuildInfo(),
				runtime.Version(),
				runtime.GOOS,
				runtime.GOARCH,
			)
			return err
		},
	}
}
