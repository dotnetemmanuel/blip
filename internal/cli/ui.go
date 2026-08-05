package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotnetemmanuel/blip/internal/build"
	"github.com/dotnetemmanuel/blip/internal/config"
	"github.com/dotnetemmanuel/blip/internal/detect"
	"github.com/dotnetemmanuel/blip/internal/output"
	"github.com/dotnetemmanuel/blip/internal/request"
	"github.com/dotnetemmanuel/blip/internal/spec"
	"github.com/dotnetemmanuel/blip/internal/theme"
	"github.com/dotnetemmanuel/blip/internal/ui"
)

const uiLong = `ui opens an interactive explorer for whatever API lives in this repo.

It finds the API on its own: .blip.toml if there is one, then a spec file, then
what the project is built with, then whatever is listening. No configuration is
required and nothing is written to the working directory.

Unlike every other blip command, ui is for humans. It emits nothing a script can
parse and refuses to start unless stdout is a terminal.`

// discoveryTimeout bounds the whole ladder. Rung 4 probes ports that are
// listening but may never answer, and the user has no way to skip a stuck one.
const discoveryTimeout = 5 * time.Second

// loadTimeout bounds a whole spec load, probing included: a host that
// accepts a connection and never answers must not leave the "loading" screen
// sitting for as long as every candidate's own per-request timeout adds up to.
const loadTimeout = 5 * time.Second

func newUICommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Browse and call this repo's API interactively",
		Long:  uiLong,
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := rt.discoveryRoot()
			if err != nil {
				return err
			}
			return ui.Run(cmd.Context(), ui.Options{
				Stdout:      rt.Stdout,
				Stdin:       rt.uiInput(),
				StdoutIsTTY: rt.StdoutIsTTY,
				Theme:       defaultTheme(),
				Discover:    discoverIn(root),
				LoadAPI:     loadAPIFor(rt),
			})
		},
	}
}

// uiInput hands the renderer a reader only when someone is typing at it; anything
// else, a typed nil included, leaves the screen drawn and unquittable.
func (rt *Runtime) uiInput() io.Reader {
	if !rt.StdinIsTTY || rt.Stdin == nil {
		return nil
	}
	return rt.Stdin
}

// loadAPIFor is ui.Options.LoadAPI: it turns a discovered target into a parsed
// API. The loader stays two-branch: read SpecPath from disk when rung 2 set
// one, otherwise use the fetcher when the target carries an Env (rung 1, 3 or
// 4), and only when neither holds is browsing refused. It never resolves
// credentials: it takes a target and nothing else, and fetchAPI leaves
// Fetcher.Authorize nil, so an authenticated spec fails to load here rather
// than reaching for a vault.
func loadAPIFor(rt *Runtime) ui.LoadAPIFunc {
	return func(ctx context.Context, target detect.Target) (*build.API, error) {
		if target.SpecPath != "" {
			data, err := os.ReadFile(target.SpecPath)
			if err != nil {
				return nil, output.WithCode(fmt.Errorf("reading %s: %w", target.SpecPath, err), output.ExitTransport)
			}
			return build.Parse(data)
		}
		if target.Env == nil {
			return nil, output.Configf(
				"%s has no spec file on disk and no base URL to probe for one; set spec_url or base_url in .blip.toml and try again",
				target.Title)
		}
		ctx, cancel := context.WithTimeout(ctx, loadTimeout)
		defer cancel()
		return fetchAPI(ctx, rt, target)
	}
}

// fetchAPI mirrors Runtime.API: the same probing, ETag cache and
// hand-declared [[route]] merging, minus the Authenticator. A caller that
// only browses must not leave behind the marker that makes a later, unrelated
// run refuse to re-probe, hence SkipNegativeCache. Warnf is bound to a
// collector rather than to stderr: bubbletea owns the screen while this runs,
// and writing to the real terminal underneath it would corrupt the frame.
func fetchAPI(ctx context.Context, rt *Runtime, target detect.Target) (*build.API, error) {
	env := target.Env

	client, err := request.NewClient(env, rt.Globals.Timeout)
	if err != nil {
		return nil, err
	}
	strict, err := request.NewStrictClient(env, rt.Globals.Timeout)
	if err != nil {
		return nil, err
	}

	apiName := target.Title
	var routes []build.Route
	if cfg, cfgErr := rt.Config(); cfgErr == nil {
		apiName = cfg.Name
		routes = declaredRoutes(cfg)
	}

	var notes []string
	fetcher := &spec.Fetcher{
		Client:            client,
		Strict:            strict,
		Refresh:           rt.Globals.Refresh,
		Offline:           rt.Globals.Offline,
		SkipNegativeCache: true,
		Warnf: func(format string, args ...any) {
			notes = append(notes, fmt.Sprintf(format, args...))
		},
	}

	s, specErr := fetcher.Load(ctx, apiName, env)

	var api *build.API
	switch {
	case specErr == nil:
		api, err = build.Parse(s.Data)
		if err != nil {
			return nil, err
		}
		// Runtime.ReportSpecWarnings only prints a freshly-parsed spec's
		// advisories (or under --verbose), and stays quiet on a cached or
		// revalidated one. Browsing must agree, or these read as permanent
		// nagging about an upstream spec that has not changed since the
		// plain CLI last saw it and said nothing.
		if s.Status != spec.StatusFetched && !rt.Globals.Verbose {
			api.Warnings = nil
		}
	case len(routes) > 0:
		// A repo whose API is entirely hand-declared must not show an empty
		// screen just because there is no spec to go with the routes.
		api = &build.API{Title: apiName}
	default:
		return nil, specErr
	}

	if err := api.AddRoutes(routes); err != nil {
		return nil, err
	}
	// notes (what came from Warnf, such as "using the cache from ...") stay
	// on their own field rather than joining api.Warnings: they are facts
	// about this load, not advice about the upstream spec, and the gate above
	// must not accidentally silence them too.
	api.LoadNotes = notes
	api.Stale = s != nil && s.Status == spec.StatusStale
	return api, nil
}

func discoverIn(root string) ui.DiscoverFunc {
	return func(ctx context.Context) ([]detect.Target, detect.Ledger, error) {
		ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
		defer cancel()
		return detect.Discover(ctx, root, &http.Client{Timeout: discoveryTimeout}, nil)
	}
}

// discoveryRoot is the directory the ladder starts from. The ladder finds its own
// config by name, so a --config under another name would send ui to a different
// API from the rest of the invocation. It refuses rather than diverge quietly.
func (rt *Runtime) discoveryRoot() (string, error) {
	if path := rt.Globals.ConfigPath; path != "" {
		if name := filepath.Base(path); !isConfigName(name) {
			return "", output.Usagef("ui finds its config by name, so --config must point at one of %v, not %s; run ui from that directory instead", config.Names, name)
		}
		return filepath.Dir(path), nil
	}
	if rt.Dir != "" {
		return rt.Dir, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", output.Usagef("cannot tell which directory to search: %v", err)
	}
	return dir, nil
}

func isConfigName(name string) bool {
	for _, known := range config.Names {
		if name == known {
			return true
		}
	}
	return false
}

// defaultTheme is the first builtin until the picker lands. Resolving an empty
// palette instead would leave every token unset, which renders with no color at all.
func defaultTheme() theme.Theme {
	lib := theme.LoadLibrary("")
	if len(lib.Themes) == 0 {
		return theme.Resolve(theme.ModeDark, theme.Palette{})
	}
	return theme.ResolveNamed(lib.Themes[0], theme.ModeDark, theme.Palette{})
}
