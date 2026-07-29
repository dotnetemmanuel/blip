package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotnetemmanuel/blip/internal/output"
)

// Set with -ldflags at release time; otherwise blip falls back to build info.
var (
	version string
	commit  string
	date    string
)

const unknown = "unknown"

// releaseAPI is where blip asks what the newest release is. A variable so a test
// can point it somewhere that is not the network.
var releaseAPI = "https://api.github.com/repos/dotnetemmanuel/blip/releases/latest"

// downloadURL is the pattern the upgrade line is built from.
const downloadURL = "https://github.com/dotnetemmanuel/blip/releases/latest/download/blip-%s-%s"

// checkTimeout bounds the release lookup. Nobody wants a version check to hang.
const checkTimeout = 10 * time.Second

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

func newVersionCommand(rt *Runtime) *cobra.Command {
	var check bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the blip version",
		Long: `Print the blip version.

--check asks GitHub what the newest release is and prints how to get it. It
downloads nothing and replaces nothing; updating stays something you run.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "blip %s (commit %s, built %s, %s %s/%s)\n",
				Version(),
				commitOrBuildInfo(),
				dateOrBuildInfo(),
				runtime.Version(),
				runtime.GOOS,
				runtime.GOARCH,
			)
			if err != nil || !check {
				return err
			}
			return rt.reportLatest(cmd.Context())
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "ask GitHub whether a newer release exists")
	return cmd
}

// reportLatest says whether a newer release exists, and how to get it.
func (rt *Runtime) reportLatest(ctx context.Context) error {
	if rt.Globals.Offline {
		return output.Configf("--offline was given, so blip cannot look up the newest release")
	}

	latest, err := latestRelease(ctx)
	if err != nil {
		return err
	}

	out := rt.Stdout
	current := Version()
	switch compareVersions(current, latest) {
	case 0:
		fmt.Fprintf(out, "%s is the newest release\n", latest)
	case 1:
		fmt.Fprintf(out, "%s is newer than the newest release, %s\n", current, latest)
	default:
		fmt.Fprintf(out, "a newer release is available: %s\n", latest)
		fmt.Fprintf(out, "  curl -fsSL -o blip "+downloadURL+" \\\n", runtime.GOOS, runtime.GOARCH)
		fmt.Fprintf(out, "    && chmod +x blip && mv blip \"$(command -v blip || echo ~/.local/bin/blip)\"\n")
	}
	return nil
}

func latestRelease(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseAPI, nil)
	if err != nil {
		return "", output.WithCode(err, output.ExitInternal)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", output.WithCode(fmt.Errorf("looking up the newest release: %w", err), output.ExitTransport)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", output.WithCode(fmt.Errorf("reading the release list: %w", err), output.ExitTransport)
	}
	if resp.StatusCode != http.StatusOK {
		return "", output.WithCode(fmt.Errorf("the release list returned %d", resp.StatusCode),
			output.ExitCodeForStatus(resp.StatusCode))
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &release); err != nil || release.TagName == "" {
		return "", output.WithCode(fmt.Errorf("the release list named no version"), output.ExitTransport)
	}
	return release.TagName, nil
}

// compareVersions orders two vX.Y.Z tags: -1 when a is older, 1 when newer, 0
// when equal or when either is not a release version, since a development build
// has nothing meaningful to compare.
func compareVersions(a, b string) int {
	x, okA := parseVersion(a)
	y, okB := parseVersion(b)
	if !okA || !okB {
		return 0
	}
	for i := range x {
		switch {
		case x[i] < y[i]:
			return -1
		case x[i] > y[i]:
			return 1
		}
	}
	return 0
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
