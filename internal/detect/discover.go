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
type Target struct {
	Title     string
	BaseURL   string
	SpecURL   string
	Env       *config.Environment
	Framework *Framework
}

// Attempt is one thing a rung of the ladder tried, and what came of it.
type Attempt struct {
	Rung    string
	Detail  string
	Outcome string
}

// Ledger records every rung's attempts, so a failure can be explained, not just reported.
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

// Rung 2 only checks these locations, in this order; a deep search risks a stray fixture.
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
	if err != nil {
		return nil, ledger, err
	}
	if len(frameworks) == 0 {
		ledger.record(RungFramework, "scanned manifests for a known framework", "none detected")
	}
	guesses := buildGuesses(repoRoot, frameworks)
	for _, g := range guesses {
		ledger.record(RungFramework, fmt.Sprintf("%s in %s", g.fw.Name, dirLabel(g.fw.Dir)), "guessed "+g.baseURL)
	}

	targets, err = probeRung(ctx, client, repoRoot, sockets, guesses, &ledger)
	if err != nil {
		return nil, ledger, err
	}
	return targets, ledger, nil
}

// discoverConfig is rung 1; a malformed .blip.toml errors rather than falls through.
func discoverConfig(repoRoot string, ledger *Ledger) ([]Target, bool, error) {
	cfg, err := config.LoadFrom(repoRoot)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			ledger.record(RungConfig, "looked for .blip.toml from "+repoRoot+" upward", "not found")
			return nil, false, nil
		}
		return nil, false, err
	}

	target, err := configTarget(cfg)
	if err != nil {
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
	var found []string

	for _, dir := range specFileDirs {
		for _, name := range specFileNames {
			path := filepath.Join(repoRoot, dir, name)
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				continue
			}
			isSpec, title, baseURL := parseSpecFile(path)
			if !isSpec {
				continue
			}
			found = append(found, filepath.Join(dir, name))
			targets = append(targets, specFileTarget(path, title, baseURL))
		}
	}

	if len(targets) == 0 {
		ledger.record(RungSpecFile, "looked for openapi/swagger.{json,yaml,yml} under ., api/, docs/, spec/", "none found")
		return nil, false
	}
	ledger.record(RungSpecFile, "spec file(s) on disk: "+strings.Join(found, ", "), "found, no framework probe needed")
	return targets, true
}

func specFileTarget(path, title, baseURL string) Target {
	if title == "" {
		title = filepath.Base(filepath.Dir(path))
	}
	return Target{
		Title:   title,
		BaseURL: baseURL,
		SpecURL: path,
		Env:     buildEnvironment(baseURL),
	}
}

// parseSpecFile reports whether path is an OpenAPI/Swagger document, with no network call.
func parseSpecFile(path string) (isSpec bool, title, baseURL string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, "", ""
	}

	var doc struct {
		OpenAPI string `json:"openapi" yaml:"openapi"`
		Swagger string `json:"swagger" yaml:"swagger"`
		Info    struct {
			Title string `json:"title" yaml:"title"`
		} `json:"info" yaml:"info"`
		Servers []struct {
			URL string `json:"url" yaml:"url"`
		} `json:"servers" yaml:"servers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return false, "", ""
		}
	}
	if doc.OpenAPI == "" && doc.Swagger == "" {
		return false, "", ""
	}
	if len(doc.Servers) > 0 {
		baseURL = doc.Servers[0].URL
	}
	return true, doc.Info.Title, baseURL
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

// orderedProbePaths puts a framework's own paths first, then blip's, each only once.
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
func probeRung(ctx context.Context, client *http.Client, repoRoot string, sockets SocketSource, guesses []guess, ledger *Ledger) ([]Target, error) {
	listeners, err := ListenersUnder(sockets, repoRoot)
	if err != nil {
		return nil, err
	}

	if len(guesses) == 0 {
		return probeListeners(ctx, client, listeners, ledger), nil
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
	return targets, nil
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
			targets = append(targets, Target{
				Title:   fmt.Sprintf("port %d", l.Port),
				BaseURL: base,
				SpecURL: outcome.specURL,
				Env:     buildEnvironment(base),
			})
		}
		ledger.record(RungProbe, detail, outcomeMessage(outcome, base))
	}
	return targets
}

func guessTarget(fw Framework, base, specURL string) Target {
	return Target{
		Title:     titleFor(fw),
		BaseURL:   base,
		SpecURL:   specURL,
		Env:       buildEnvironment(base),
		Framework: &fw,
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

func buildEnvironment(base string) *config.Environment {
	if base == "" {
		return nil
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil
	}
	return &config.Environment{
		Name:     "discovered",
		BaseURL:  u,
		Insecure: config.IsLocalHost(u.Hostname()),
		Timeout:  config.DefaultTimeout,
	}
}

// probeOutcome keeps found, reached and tlsRefused separate for a precise ledger.
type probeOutcome struct {
	specURL    string
	found      bool
	reached    bool
	tlsRefused bool
}

func outcomeMessage(o probeOutcome, base string) string {
	switch {
	case o.found:
		return "found a spec at " + o.specURL
	case o.tlsRefused:
		return fmt.Sprintf("certificate rejected: %s is not loopback, self-signed not accepted", hostOf(base))
	case o.reached:
		return "reached, served no spec"
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
		status, body, err := getCandidate(ctx, client, candidate)
		if err != nil {
			if isCertRefusal(err) {
				out.tlsRefused = true
			}
			continue
		}
		out.reached = true
		if status == http.StatusOK && spec.LooksLikeSpec(body) {
			out.specURL = candidate
			out.found = true
			return out
		}
	}
	return out
}

func getCandidate(ctx context.Context, client *http.Client, candidate string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, candidate, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json, application/yaml;q=0.9, */*;q=0.5")

	resp, err := clientForCandidate(client, candidate).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

func isCertRefusal(err error) bool {
	var unknownAuthority x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	return errors.As(err, &unknownAuthority) || errors.As(err, &invalid) || errors.As(err, &hostname)
}

// clientForCandidate relaxes TLS only for an https loopback candidate.
func clientForCandidate(base *http.Client, candidate string) *http.Client {
	u, err := url.Parse(candidate)
	if err != nil || u.Scheme != "https" || !config.IsLocalHost(u.Hostname()) {
		return base
	}
	return insecureVariant(base)
}

func insecureVariant(base *http.Client) *http.Client {
	clone := *base

	transport, ok := base.Transport.(*http.Transport)
	if !ok || transport == nil {
		def, _ := http.DefaultTransport.(*http.Transport)
		if def == nil {
			def = &http.Transport{}
		}
		transport = def
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

	clone.Transport = transport
	return &clone
}
