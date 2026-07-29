package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"

	"github.com/dotnetemmanuel/blip/internal/auth"
	"github.com/dotnetemmanuel/blip/internal/build"
	"github.com/dotnetemmanuel/blip/internal/config"
	"github.com/dotnetemmanuel/blip/internal/creds"
	"github.com/dotnetemmanuel/blip/internal/output"
	"github.com/dotnetemmanuel/blip/internal/request"
	"github.com/dotnetemmanuel/blip/internal/spec"
	"github.com/dotnetemmanuel/blip/internal/xdg"
)

// Runtime carries everything a command needs, resolving config, environment and
// credentials lazily so that --help and describe never reach for a vault.
type Runtime struct {
	Globals *Globals

	Stdout io.Writer
	Stderr io.Writer
	Stdin  *os.File

	// Input is where a request body or a confirmation is read from. It defaults
	// to Stdin; tests supply their own.
	Input io.Reader

	StdinIsTTY  bool
	StdoutIsTTY bool

	// Dir is where config discovery starts. Empty means the process working directory.
	Dir string

	configOnce sync.Once
	config     *config.Config
	configErr  error

	envOnce sync.Once
	env     *config.Environment
	envErr  error

	authOnce sync.Once
	auth     auth.Authenticator
	authErr  error

	clientOnce sync.Once
	client     *http.Client
	clientErr  error

	specOnce sync.Once
	spec     *spec.Spec
	specErr  error

	apiOnce sync.Once
	api     *build.API
	apiErr  error

	warningsShown bool
}

// Warnf writes a diagnostic to stderr. Nothing blip warns about belongs on stdout.
func (rt *Runtime) Warnf(format string, args ...any) {
	fmt.Fprintf(rt.Stderr, "blip: "+format+"\n", args...)
}

// Verbosef writes a diagnostic only under --verbose.
func (rt *Runtime) Verbosef(format string, args ...any) {
	if rt.Globals.Verbose {
		rt.Warnf(format, args...)
	}
}

// Config returns the discovered .blip.toml.
func (rt *Runtime) Config() (*config.Config, error) {
	rt.configOnce.Do(func() {
		if rt.Globals.ConfigPath != "" {
			rt.config, rt.configErr = config.Load(rt.Globals.ConfigPath)
			return
		}
		dir := rt.Dir
		if dir == "" {
			dir, _ = os.Getwd()
		}
		rt.config, rt.configErr = config.LoadFrom(dir)
	})
	return rt.config, rt.configErr
}

// Env returns the resolved environment.
func (rt *Runtime) Env() (*config.Environment, error) {
	rt.envOnce.Do(func() {
		cfg, err := rt.Config()
		if err != nil {
			rt.envErr = err
			return
		}
		rt.env, rt.envErr = cfg.ResolveEnv(rt.Globals.Env)
	})
	return rt.env, rt.envErr
}

// Client returns the HTTP client for the resolved environment.
func (rt *Runtime) Client() (*http.Client, error) {
	rt.clientOnce.Do(func() {
		env, err := rt.Env()
		if err != nil {
			rt.clientErr = err
			return
		}
		rt.client, rt.clientErr = request.NewClient(env, rt.Globals.Timeout)
	})
	return rt.client, rt.clientErr
}

// Spec returns the OpenAPI document for the resolved environment.
func (rt *Runtime) Spec(ctx context.Context) (*spec.Spec, error) {
	rt.specOnce.Do(func() {
		cfg, err := rt.Config()
		if err != nil {
			rt.specErr = err
			return
		}
		env, err := rt.Env()
		if err != nil {
			rt.specErr = err
			return
		}
		client, err := rt.Client()
		if err != nil {
			rt.specErr = err
			return
		}

		fetcher := &spec.Fetcher{
			Client:  client,
			Refresh: rt.Globals.Refresh,
			Offline: rt.Globals.Offline,
			Warnf:   rt.Warnf,
			Authorize: func(ctx context.Context, req *http.Request) error {
				a, err := rt.Authenticator(ctx)
				if err != nil {
					return err
				}
				return a.Apply(ctx, req)
			},
		}
		rt.spec, rt.specErr = fetcher.Load(ctx, cfg.Name, env)
		if rt.spec != nil {
			rt.Verbosef("spec %s from %s", rt.spec.Status, rt.spec.Meta.URL)
		}
	})
	return rt.spec, rt.specErr
}

// API returns the parsed command model for the resolved environment, merged with
// any hand-declared routes.
func (rt *Runtime) API(ctx context.Context) (*build.API, error) {
	rt.apiOnce.Do(func() {
		cfg, err := rt.Config()
		if err != nil {
			rt.apiErr = err
			return
		}

		var api *build.API
		s, specErr := rt.Spec(ctx)
		switch {
		case specErr == nil:
			api, rt.apiErr = build.Parse(s.Data)
			if rt.apiErr != nil {
				return
			}
			defer func() { rt.ReportSpecWarnings(api, s.Status == spec.StatusFetched) }()
		case len(cfg.Routes) > 0:
			// Hand-declared routes are the whole point of working without a spec.
			rt.Verbosef("no spec (%v), using the %d declared routes", specErr, len(cfg.Routes))
			api = &build.API{Title: cfg.Name}
		default:
			rt.apiErr = specErr
			return
		}

		if rt.apiErr = api.AddRoutes(declaredRoutes(cfg)); rt.apiErr != nil {
			return
		}
		rt.api = api
	})
	return rt.api, rt.apiErr
}

// ReportSpecWarnings puts spec problems on stderr when they are new, so a run
// against an unchanged spec stays quiet. It reports at most once per process.
func (rt *Runtime) ReportSpecWarnings(api *build.API, force bool) {
	if rt.warningsShown || api == nil {
		return
	}
	if !force && !rt.Globals.Verbose {
		return
	}
	rt.warningsShown = true
	for _, w := range api.Warnings {
		rt.Warnf("%s", w)
	}
}

// ProfileName is the credentials profile in force: --profile if given, otherwise
// whatever the environment names.
func (rt *Runtime) ProfileName() (string, error) {
	if rt.Globals.Profile != "" {
		return rt.Globals.Profile, nil
	}
	env, err := rt.Env()
	if err != nil {
		return "", err
	}
	return env.Auth, nil
}

// Authenticator resolves credentials. It is called only when a request is about
// to be sent, so a vault is never consulted for --help or describe.
func (rt *Runtime) Authenticator(ctx context.Context) (auth.Authenticator, error) {
	rt.authOnce.Do(func() {
		rt.auth, rt.authErr = rt.buildAuthenticator(ctx)
	})
	return rt.auth, rt.authErr
}

func (rt *Runtime) buildAuthenticator(ctx context.Context) (auth.Authenticator, error) {
	profile, err := rt.ProfileName()
	if err != nil {
		return nil, err
	}
	if profile == "" {
		return auth.None(), nil
	}

	store, err := creds.Load()
	if errors.Is(err, creds.ErrNoFile) {
		path, _ := xdg.CredentialsPath()
		return nil, output.WithCode(fmt.Errorf(
			"profile %q is configured but there is no credentials file at %s", profile, path), output.ExitAuth)
	}
	if err != nil {
		return nil, err
	}

	resolved, err := store.Resolve(ctx, profile, creds.NewResolver(rt.interactiveStdin(), rt.stderrFile()))
	if err != nil {
		return nil, err
	}

	client, err := rt.tokenClient(resolved.TokenURL)
	if err != nil {
		return nil, err
	}
	return auth.New(resolved, client)
}

// tokenClient is the client an oauth2_cc profile uses to reach its identity
// provider. insecure was granted for a local development certificate on the API,
// so it only carries over when the token endpoint is local too.
func (rt *Runtime) tokenClient(tokenURL string) (*http.Client, error) {
	env, err := rt.Env()
	if err != nil {
		return nil, err
	}
	if tokenURL == "" || config.IsLocalHost(hostOf(tokenURL)) {
		return rt.Client()
	}
	return request.NewStrictClient(env, rt.Globals.Timeout)
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// Redact removes any secret blip has already resolved from a message on its way
// out. It never triggers resolution: printing an error must not reach a vault.
func (rt *Runtime) Redact(s string) string {
	if rt.auth == nil {
		return s
	}
	return output.NewRedactor(rt.auth.Secrets()).String(s)
}

// stderrFile lets a vault command write its prompts where the user can see them,
// but only when blip's own stderr really is the terminal.
func (rt *Runtime) stderrFile() *os.File {
	if f, ok := rt.Stderr.(*os.File); ok {
		return f
	}
	return nil
}

// input is where --data @- and the confirmation prompt read from.
func (rt *Runtime) input() io.Reader {
	if rt.Input != nil {
		return rt.Input
	}
	if rt.Stdin == nil {
		return nil
	}
	return rt.Stdin
}

// interactiveStdin is handed to vault commands only at a terminal: a pinentry
// prompt is worth waiting for, a piped stdin is data meant for the request body.
func (rt *Runtime) interactiveStdin() *os.File {
	if rt.StdinIsTTY {
		return rt.Stdin
	}
	return nil
}

// declaredRoutes converts [[route]] config entries into the build model.
func declaredRoutes(cfg *config.Config) []build.Route {
	routes := make([]build.Route, 0, len(cfg.Routes))
	for _, r := range cfg.Routes {
		routes = append(routes, build.Route{
			Name:    r.Name,
			Method:  r.Method,
			Path:    r.Path,
			Summary: r.Summary,
			Group:   r.Group,
		})
	}
	return routes
}
