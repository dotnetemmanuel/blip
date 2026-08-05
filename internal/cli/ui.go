package cli

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotnetemmanuel/blip/internal/build"
	"github.com/dotnetemmanuel/blip/internal/config"
	"github.com/dotnetemmanuel/blip/internal/detect"
	"github.com/dotnetemmanuel/blip/internal/output"
	"github.com/dotnetemmanuel/blip/internal/request"
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
				LoadAPI:     loadAPI,
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

// loadTimeout bounds a single spec fetch, same order of magnitude as discovery.
const loadTimeout = 5 * time.Second

// loadAPI is ui.Options.LoadAPI: it turns a discovered target into a parsed
// API. It takes a target and nothing else, so it has no credential source to
// reach for even by mistake; an authenticated spec therefore fails to load
// here, and that failure is reported on screen rather than worked around.
func loadAPI(ctx context.Context, target detect.Target) (*build.API, error) {
	if target.SpecPath != "" {
		data, err := os.ReadFile(target.SpecPath)
		if err != nil {
			return nil, output.WithCode(fmt.Errorf("reading %s: %w", target.SpecPath, err), output.ExitTransport)
		}
		return build.Parse(data)
	}
	if target.SpecURL == "" {
		return nil, output.Configf("%s has no known spec location to browse", target.Title)
	}

	ctx, cancel := context.WithTimeout(ctx, loadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.SpecURL, nil)
	if err != nil {
		return nil, output.WithCode(fmt.Errorf("building a request for %s: %w", target.SpecURL, err), output.ExitInternal)
	}
	req.Header.Set("Accept", "application/json, application/yaml;q=0.9, */*;q=0.5")

	resp, err := unauthenticatedSpecClient(target.SpecURL).Do(req)
	if err != nil {
		return nil, output.WithCode(fmt.Errorf("fetching %s: %w", target.SpecURL, err), output.ExitTransport)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, output.WithCode(fmt.Errorf("reading the response from %s: %w", target.SpecURL, err), output.ExitTransport)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, output.WithCode(fmt.Errorf("%s returned %d", target.SpecURL, resp.StatusCode), output.ExitTransport)
	}
	return build.Parse(body)
}

// unauthenticatedSpecClient relaxes TLS only for a spec URL whose own host is
// loopback, and always applies the redirect policy so that relaxation cannot
// be carried to somewhere else by a redirect.
func unauthenticatedSpecClient(specURL string) *http.Client {
	client := &http.Client{Timeout: loadTimeout, CheckRedirect: request.CheckRedirect}

	u, err := url.Parse(specURL)
	if err != nil || u.Scheme != "https" || !config.IsLocalHost(u.Hostname()) {
		return client
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return client
	}
	t := transport.Clone()
	t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	client.Transport = t
	return client
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
