package detect

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dotnetemmanuel/blip/internal/config"
	"github.com/dotnetemmanuel/blip/internal/request"
	"github.com/dotnetemmanuel/blip/internal/spec"
)

// Rung labels, in the order the ladder tries them.
const (
	RungConfig    = "config"
	RungSpecFile  = "spec-file"
	RungFramework = "framework"
	RungProbe     = "probe"
)

// Target is one API discovery found, ready to browse or send against.
// BaseURL == "" if and only if Env == nil if and only if Unsendable != "".
type Target struct {
	Title      string
	BaseURL    string
	SpecURL    string
	SpecPath   string
	Env        *config.Environment
	Unsendable string
	Framework  *Framework
}

// Attempt is one thing a rung of the ladder tried, and what came of it.
type Attempt struct {
	Rung    string
	Detail  string
	Outcome string
}

// Ledger records every rung's attempts, so a failure can be explained.
type Ledger struct {
	Attempts []Attempt
}

func (l *Ledger) record(rung, detail, outcome string) {
	l.Attempts = append(l.Attempts, Attempt{Rung: rung, Detail: detail, Outcome: outcome})
}

// String renders the ledger as one sentence, rung by rung.
func (l Ledger) String() string {
	parts := make([]string, 0, len(l.Attempts))
	for _, a := range l.Attempts {
		parts = append(parts, fmt.Sprintf("%s: %s", a.Detail, a.Outcome))
	}
	return strings.Join(parts, "; ")
}

// Rung 2 only checks these locations; a deep search risks a stray fixture.
var specFileNames = []string{
	"openapi.json", "openapi.yaml", "openapi.yml",
	"swagger.json", "swagger.yaml", "swagger.yml",
}

var specFileDirs = []string{".", "api", "docs", "spec"}

// Discover walks the ladder for repoRoot, stopping at the first rung that succeeds.
func Discover(ctx context.Context, repoRoot string, client *http.Client, sockets SocketSource) ([]Target, Ledger, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if sockets == nil {
		sockets = NewSocketSource()
	}
	var ledger Ledger

	targets, ok, err := discoverConfig(repoRoot, &ledger)
	if err != nil {
		return nil, ledger, err
	}
	if ok {
		return targets, ledger, nil
	}

	if targets, ok := discoverSpecFiles(repoRoot, &ledger); ok {
		return targets, ledger, nil
	}

	frameworks, err := DetectFrameworks(repoRoot)
	switch {
	case err != nil:
		// Below rung 1 is inference, so a scan failure is a ledger line, not an abort.
		ledger.record(RungFramework, "scanned manifests for a known framework", "failed: "+err.Error())
	case len(frameworks) == 0:
		ledger.record(RungFramework, "scanned manifests for a known framework", "none detected")
	}
	guesses := buildGuesses(repoRoot, frameworks)
	for _, g := range guesses {
		ledger.record(RungFramework, fmt.Sprintf("%s in %s", g.fw.Name, dirLabel(g.fw.Dir)), "guessed "+g.baseURL)
	}

	return probeRung(ctx, client, repoRoot, sockets, guesses, &ledger), ledger, nil
}

// discoverConfig is rung 1; a malformed .blip.toml is an error, not a fall-through.
func discoverConfig(repoRoot string, ledger *Ledger) ([]Target, bool, error) {
	detail := "looked for .blip.toml from " + repoRoot + " upward"
	cfg, err := config.LoadFrom(repoRoot)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			ledger.record(RungConfig, detail, "not found")
			return nil, false, nil
		}
		ledger.record(RungConfig, detail, "failed: "+err.Error())
		return nil, false, err
	}

	target, err := configTarget(cfg)
	if err != nil {
		ledger.record(RungConfig, ".blip.toml at "+cfg.Path, "failed: "+err.Error())
		return nil, false, err
	}
	ledger.record(RungConfig, ".blip.toml at "+cfg.Path, "found, used directly")
	return []Target{target}, true, nil
}

func configTarget(cfg *config.Config) (Target, error) {
	env, err := cfg.ResolveEnv("")
	if err != nil {
		return Target{}, err
	}
	return Target{
		Title:   cfg.Name,
		BaseURL: env.BaseURL.String(),
		SpecURL: env.SpecURL,
		Env:     env,
	}, nil
}

// discoverSpecFiles is rung 2: a document already on disk, no network involved.
func discoverSpecFiles(repoRoot string, ledger *Ledger) ([]Target, bool) {
	var targets []Target

	for _, dir := range specFileDirs {
		for _, name := range specFileNames {
			path := filepath.Join(repoRoot, dir, name)
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				continue
			}
			isSpec, title, servers := parseSpecFile(path)
			if !isSpec {
				continue
			}
			target := specFileTarget(path, title, servers)
			targets = append(targets, target)

			rel := filepath.Join(dir, name)
			if target.Unsendable != "" {
				ledger.record(RungSpecFile, "spec file "+rel, target.Unsendable)
			} else {
				ledger.record(RungSpecFile, "spec file "+rel, "adopted server "+target.BaseURL)
			}
		}
	}

	if len(targets) == 0 {
		ledger.record(RungSpecFile, "looked for openapi/swagger.{json,yaml,yml} under ., api/, docs/, spec/", "none found")
		return nil, false
	}
	return targets, true
}

func specFileTarget(path, title string, servers []string) Target {
	if title == "" {
		title = filepath.Base(filepath.Dir(path))
	}
	picked, unsendable := pickServer(servers)
	if unsendable != "" {
		return Target{Title: title, SpecPath: path, Unsendable: unsendable}
	}
	baseURL, env, unsendable := sendability(picked)
	return Target{Title: title, BaseURL: baseURL, SpecPath: path, Env: env, Unsendable: unsendable}
}

// pickServer prefers loopback over silently adopting a production server.
func pickServer(servers []string) (base, unsendable string) {
	if len(servers) == 0 {
		return "", "this spec file names no server"
	}
	var fallback string
	for _, raw := range servers {
		u, err := url.Parse(raw)
		if err != nil || !u.IsAbs() || u.Host == "" || strings.ContainsAny(raw, "{}") {
			continue
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			continue
		}
		if config.IsLocalHost(u.Hostname()) {
			return raw, ""
		}
		if fallback == "" {
			fallback = raw
		}
	}
	if fallback != "" {
		return fallback, ""
	}
	return "", "this spec file names no usable server (relative, templated, or missing a host)"
}

// parseSpecFile reports whether path is an OpenAPI/Swagger document.
func parseSpecFile(path string) (isSpec bool, title string, servers []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, "", nil
	}

	var doc struct {
		OpenAPI any `json:"openapi" yaml:"openapi"`
		Swagger any `json:"swagger" yaml:"swagger"`
		Info    struct {
			Title string `json:"title" yaml:"title"`
		} `json:"info" yaml:"info"`
		Servers []struct {
			URL string `json:"url" yaml:"url"`
		} `json:"servers" yaml:"servers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return false, "", nil
		}
	}
	if doc.OpenAPI == nil && doc.Swagger == nil {
		return false, "", nil
	}
	for _, s := range doc.Servers {
		servers = append(servers, s.URL)
	}
	return true, doc.Info.Title, servers
}

// guess is one rung-3 candidate: a base URL and framework-ordered spec paths.
type guess struct {
	fw      Framework
	baseURL string
	paths   []string
}

func buildGuesses(repoRoot string, frameworks []Framework) []guess {
	guesses := make([]guess, 0, len(frameworks))
	for _, fw := range frameworks {
		profile, ok := ReadRunProfile(repoRoot, fw)
		baseURL := ""
		if ok {
			baseURL = profile.BaseURL
		}
		if baseURL == "" {
			baseURL = fmt.Sprintf("http://localhost:%d", fw.DefaultPort)
		}
		guesses = append(guesses, guess{fw: fw, baseURL: baseURL, paths: orderedProbePaths(fw.SpecPaths)})
	}
	return guesses
}

// orderedProbePaths puts a framework's paths first, then blip's, each once.
func orderedProbePaths(first []string) []string {
	seen := make(map[string]bool, len(first)+len(spec.ProbePaths)+len(spec.ExtraProbePaths))
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range first {
		add(p)
	}
	for _, p := range spec.ProbePaths {
		add(p)
	}
	for _, p := range spec.ExtraProbePaths {
		add(p)
	}
	return out
}

// probeRung is rung 4; a matching listener corrects a guess's stale port.
func probeRung(ctx context.Context, client *http.Client, repoRoot string, sockets SocketSource, guesses []guess, ledger *Ledger) []Target {
	listeners, err := ListenersUnder(sockets, repoRoot)
	if err != nil {
		ledger.record(RungProbe, "looked for listening ports owned by this repo", "failed: "+err.Error())
		listeners = nil
	}

	if len(guesses) == 0 {
		return probeListeners(ctx, client, listeners, ledger)
	}

	var targets []Target
	for _, g := range guesses {
		base := g.baseURL
		if l, ok := listenerFor(repoRoot, g.fw, listeners); ok {
			base = fmt.Sprintf("http://localhost:%d", l.Port)
		}
		detail := fmt.Sprintf("%s in %s, expected %s at %s", g.fw.Name, dirLabel(g.fw.Dir), strings.Join(g.paths, ", "), base)
		outcome := probeGuess(ctx, client, base, g.paths)
		if outcome.found {
			targets = append(targets, guessTarget(g.fw, base, outcome.specURL))
		}
		ledger.record(RungProbe, detail, outcomeMessage(outcome, base))
	}
	return targets
}

// listenerFor matches a listener to fw's own directory.
func listenerFor(repoRoot string, fw Framework, listeners []Listener) (Listener, bool) {
	want := repoRoot
	if fw.Dir != "." {
		want = filepath.Join(repoRoot, fw.Dir)
	}
	wantCanon, err := canonical(want)
	if err != nil {
		return Listener{}, false
	}
	for _, l := range listeners {
		if filepath.Clean(l.Cwd) == wantCanon {
			return l, true
		}
	}
	return Listener{}, false
}

// probeListeners is rung 4 with no framework guess at all.
func probeListeners(ctx context.Context, client *http.Client, listeners []Listener, ledger *Ledger) []Target {
	if len(listeners) == 0 {
		ledger.record(RungProbe, "no framework detected; looked for listening ports owned by this repo", "none listening")
		return nil
	}

	paths := orderedProbePaths(nil)
	var targets []Target
	for _, l := range listeners {
		base := fmt.Sprintf("http://localhost:%d", l.Port)
		detail := fmt.Sprintf("no framework detected; probed the listening port %d", l.Port)
		outcome := probeGuess(ctx, client, base, paths)
		if outcome.found {
			targets = append(targets, listenerTarget(l, outcome.specURL))
		}
		ledger.record(RungProbe, detail, outcomeMessage(outcome, base))
	}
	return targets
}

func listenerTarget(l Listener, specURL string) Target {
	base := fmt.Sprintf("http://localhost:%d", l.Port)
	baseURL, env, unsendable := sendability(base)
	return Target{
		Title:      fmt.Sprintf("port %d", l.Port),
		BaseURL:    baseURL,
		SpecURL:    specURL,
		Env:        env,
		Unsendable: unsendable,
	}
}

func guessTarget(fw Framework, base, specURL string) Target {
	baseURL, env, unsendable := sendability(base)
	return Target{
		Title:      titleFor(fw),
		BaseURL:    baseURL,
		SpecURL:    specURL,
		Env:        env,
		Unsendable: unsendable,
		Framework:  &fw,
	}
}

func titleFor(fw Framework) string {
	if fw.Dir != "." {
		return fw.Dir
	}
	return fw.Name
}

func dirLabel(dir string) string {
	if dir == "." {
		return "the repo root"
	}
	return dir
}

// sendability sets BaseURL, Env and Unsendable together, keeping Target's invariant.
func sendability(base string) (baseURL string, env *config.Environment, unsendable string) {
	if base == "" {
		return "", nil, "no base URL is known"
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", nil, fmt.Sprintf("%q is not a usable base URL", base)
	}
	return base, &config.Environment{
		Name:     "discovered",
		BaseURL:  u,
		Insecure: config.IsLocalHost(u.Hostname()),
		Timeout:  config.DefaultTimeout,
	}, ""
}

// probeOutcome keeps each cause separate, so the ledger states, not guesses.
type probeOutcome struct {
	specURL         string
	found           bool
	reached         bool
	tlsRefused      bool
	redirectRefused bool
	lastErr         error
}

func outcomeMessage(o probeOutcome, base string) string {
	switch {
	case o.found:
		return "found a spec at " + o.specURL
	case o.redirectRefused:
		return "redirect refused: " + o.lastErr.Error()
	case o.tlsRefused:
		host := hostOf(base)
		if !config.IsLocalHost(host) {
			return fmt.Sprintf("certificate rejected: %s is not loopback, self-signed not accepted", host)
		}
		return fmt.Sprintf("certificate rejected at %s: TLS could not be relaxed for this client", host)
	case o.reached:
		return "reached, served no spec"
	case o.lastErr != nil:
		return "failed: " + o.lastErr.Error()
	default:
		return "nothing listening there"
	}
}

func hostOf(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	return u.Hostname()
}

// probeGuess tries each path against base in order, stopping at the first document.
func probeGuess(ctx context.Context, client *http.Client, base string, paths []string) probeOutcome {
	var out probeOutcome
	for _, p := range paths {
		candidate := strings.TrimSuffix(base, "/") + p
		status, body, finalURL, err := getCandidate(ctx, client, candidate)
		if err != nil {
			out.lastErr = err
			switch {
			case errors.Is(err, request.ErrRedirectRefused):
				out.redirectRefused = true
			case isCertRefusal(err):
				out.tlsRefused = true
			}
			continue
		}
		out.reached = true
		if status == http.StatusOK && spec.LooksLikeSpec(body) {
			out.specURL = finalURL
			out.found = true
			return out
		}
	}
	return out
}

// getCandidate returns the URL that actually answered, not the one requested.
func getCandidate(ctx context.Context, client *http.Client, candidate string) (status int, body []byte, finalURL string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, candidate, nil)
	if err != nil {
		return 0, nil, "", err
	}
	req.Header.Set("Accept", "application/json, application/yaml;q=0.9, */*;q=0.5")

	resp, err := clientForCandidate(client, candidate).Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, "", err
	}

	final := candidate
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return resp.StatusCode, body, final, nil
}

func isCertRefusal(err error) bool {
	var unknownAuthority x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	return errors.As(err, &unknownAuthority) || errors.As(err, &invalid) || errors.As(err, &hostname)
}

// clientForCandidate always applies the redirect policy, and relaxes loopback https.
func clientForCandidate(base *http.Client, candidate string) *http.Client {
	u, err := url.Parse(candidate)
	if err == nil && u.Scheme == "https" && config.IsLocalHost(u.Hostname()) {
		if relaxed, ok := insecureVariant(base); ok {
			return relaxed
		}
	}
	return withRedirectPolicy(base)
}

// withRedirectPolicy stops any probe, relaxed or not, from being redirected off host.
func withRedirectPolicy(base *http.Client) *http.Client {
	clone := *base
	clone.CheckRedirect = request.CheckRedirect
	return &clone
}

// ok is false rather than substituting a transport, so a caller's is never dropped.
func insecureVariant(base *http.Client) (client *http.Client, ok bool) {
	var transport *http.Transport
	switch t := base.Transport.(type) {
	case *http.Transport:
		transport = t
	case nil:
		transport, _ = http.DefaultTransport.(*http.Transport)
	default:
		return nil, false
	}
	if transport == nil {
		return nil, false
	}
	transport = transport.Clone()

	tlsConfig := transport.TLSClientConfig
	if tlsConfig != nil {
		tlsConfig = tlsConfig.Clone()
	} else {
		tlsConfig = &tls.Config{}
	}
	tlsConfig.InsecureSkipVerify = true
	transport.TLSClientConfig = tlsConfig

	clone := *base
	clone.Transport = transport
	clone.CheckRedirect = request.CheckRedirect
	return &clone, true
}
