package detect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func writeAspNetFixture(t *testing.T, dir, applicationURL string) {
	t.Helper()
	csproj := `<Project Sdk="Microsoft.NET.Sdk.Web">
  <ItemGroup>
    <PackageReference Include="Microsoft.AspNetCore.OpenApi" Version="10.0.9" />
  </ItemGroup>
</Project>
`
	if err := os.WriteFile(filepath.Join(dir, "Api.csproj"), []byte(csproj), 0o644); err != nil {
		t.Fatal(err)
	}
	if applicationURL == "" {
		return
	}
	if err := os.MkdirAll(filepath.Join(dir, "Properties"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := fmt.Sprintf(`{"profiles":{"Api":{"commandName":"Project","applicationUrl":%q}}}`, applicationURL)
	if err := os.WriteFile(filepath.Join(dir, "Properties", "launchSettings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFastAPIFixture(t *testing.T, dir string) {
	t.Helper()
	pyproject := `[project]
name = "catalog"
dependencies = ["fastapi>=0.115"]
`
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(pyproject), 0o644); err != nil {
		t.Fatal(err)
	}
}

// failingTransport fails the test if a request is ever sent through it.
type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.t.Fatalf("unexpected network call to %s", req.URL)
	return nil, nil
}

func noNetworkClient(t *testing.T) *http.Client {
	return &http.Client{Transport: failingTransport{t}}
}

func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	addr, ok := srv.Listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener address type %T", srv.Listener.Addr())
	}
	return addr.Port
}

func openAPIHandler(path string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openapi":"3.0.0","info":{"title":"Api","version":"1.0"},"paths":{}}`))
	})
	return mux
}

func notFoundHandler() http.Handler {
	return http.NewServeMux() // an empty mux 404s everything
}

// newSilentTLSServer keeps an expected handshake failure out of the test log.
func newSilentTLSServer(handler http.Handler) *httptest.Server {
	srv := httptest.NewUnstartedServer(handler)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	return srv
}

// pathRecorder records the order requests arrived in, for asserting probe order.
type pathRecorder struct {
	mu    sync.Mutex
	paths []string
}

func (r *pathRecorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.paths = append(r.paths, req.URL.Path)
		r.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	}
}

func (r *pathRecorder) indexOf(path string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, p := range r.paths {
		if p == path {
			return i
		}
	}
	return -1
}

// redirectClient dials target regardless of the requested host.
func redirectClient(target string) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}
	return &http.Client{Transport: transport}
}

// hostMappingClient lets a fake hostname resolve to a real listener address.
func hostMappingClient(hostAddr map[string]string) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if host, _, err := net.SplitHostPort(addr); err == nil {
			if mapped, ok := hostAddr[host]; ok {
				addr = mapped
			}
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	return &http.Client{Transport: transport}
}

// spyTransport is not a *http.Transport, so relaxation must refuse, not substitute.
type spyTransport struct {
	inner http.RoundTripper
	calls int
}

func (s *spyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.calls++
	return s.inner.RoundTrip(req)
}

func TestDiscoverConfigWins(t *testing.T) {
	root := testdata("discover/config-wins")
	targets, ledger, err := Discover(context.Background(), root, noNetworkClient(t), fakeSource{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("got %d targets, want 1: %+v", len(targets), targets)
	}
	if targets[0].Title != "configwins" {
		t.Errorf("Title = %q, want %q", targets[0].Title, "configwins")
	}
	if targets[0].BaseURL != "http://localhost:9" {
		t.Errorf("BaseURL = %q, want %q", targets[0].BaseURL, "http://localhost:9")
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Rung != RungConfig {
		t.Fatalf("ledger = %+v, want exactly one config attempt (the spec file must never be consulted)", ledger.Attempts)
	}
}

func TestDiscoverConfigWalksUpFromSubdirectory(t *testing.T) {
	root := testdata("discover/config-walkup/sub")
	targets, _, err := Discover(context.Background(), root, noNetworkClient(t), fakeSource{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(targets) != 1 || targets[0].Title != "walkup" {
		t.Fatalf("got %+v, want the config found by walking up from the subdirectory", targets)
	}
}

func TestDiscoverSpecFileNoNetworkCall(t *testing.T) {
	root := testdata("discover/spec-file-only")
	targets, ledger, err := Discover(context.Background(), root, noNetworkClient(t), fakeSource{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("got %d targets, want 1: %+v", len(targets), targets)
	}
	got := targets[0]
	wantPath := filepath.Join(root, "openapi.json")
	if got.SpecPath != wantPath {
		t.Errorf("SpecPath = %q, want %q", got.SpecPath, wantPath)
	}
	if got.SpecURL != "" {
		t.Errorf("SpecURL = %q, want empty: rung 2 finds a file, not a URL", got.SpecURL)
	}
	if got.Title != "Test API" {
		t.Errorf("Title = %q, want %q", got.Title, "Test API")
	}
	// This fixture's document names no server, so it must be unsendable.
	if got.BaseURL != "" || got.Env != nil || got.Unsendable == "" {
		t.Errorf("got BaseURL=%q Env=%v Unsendable=%q, want the no-server unsendable state", got.BaseURL, got.Env, got.Unsendable)
	}
	if len(ledger.Attempts) != 2 {
		t.Errorf("ledger = %+v, want a config miss then a spec-file hit", ledger.Attempts)
	}
}

func TestDiscoverRung4Hit(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(openAPIHandler("/openapi/v1.json"))
	defer srv.Close()
	writeAspNetFixture(t, dir, srv.URL)

	targets, ledger, err := Discover(context.Background(), dir, srv.Client(), fakeSource{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("got %d targets, want 1: %+v", len(targets), targets)
	}
	wantSpec := srv.URL + "/openapi/v1.json"
	if targets[0].SpecURL != wantSpec {
		t.Errorf("SpecURL = %q, want %q", targets[0].SpecURL, wantSpec)
	}

	port := strconv.Itoa(serverPort(t, srv))
	found := false
	for _, a := range ledger.Attempts {
		if a.Rung == RungProbe && strings.Contains(a.Outcome, wantSpec) && strings.Contains(a.Detail, port) {
			found = true
		}
	}
	if !found {
		t.Errorf("ledger = %+v, want an attempt naming port %s and the found spec", ledger.Attempts, port)
	}
}

func TestDiscoverRung4ReachedButNoSpec(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(notFoundHandler())
	defer srv.Close()
	writeAspNetFixture(t, dir, srv.URL)

	targets, ledger, err := Discover(context.Background(), dir, srv.Client(), fakeSource{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("got %d targets, want 0: %+v", len(targets), targets)
	}
	found := false
	for _, a := range ledger.Attempts {
		if a.Rung == RungProbe && a.Outcome == "reached, served no spec" {
			found = true
		}
	}
	if !found {
		t.Errorf("ledger = %+v, want an attempt saying the port was reached and served no spec", ledger.Attempts)
	}
}

func TestDiscoverRung4WrongGuessRecovered(t *testing.T) {
	dir := t.TempDir()
	// The run profile names a port nothing is listening on.
	writeAspNetFixture(t, dir, "https://127.0.0.1:1")

	srv := httptest.NewServer(openAPIHandler("/openapi/v1.json"))
	defer srv.Close()

	canonDir, err := canonical(dir)
	if err != nil {
		t.Fatal(err)
	}
	sockets := fakeSource{listeners: []Listener{{Port: serverPort(t, srv), PID: 1, Cwd: canonDir}}}

	targets, _, err := Discover(context.Background(), dir, srv.Client(), sockets)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("got %d targets, want 1 (the socket source should have corrected the wrong port): %+v", len(targets), targets)
	}
	wantBase := fmt.Sprintf("http://localhost:%d", serverPort(t, srv))
	if targets[0].BaseURL != wantBase {
		t.Errorf("BaseURL = %q, want %q", targets[0].BaseURL, wantBase)
	}
}

func TestDiscoverProbeOrderFollowsDetection(t *testing.T) {
	t.Run("fastapi tries openapi.json first", func(t *testing.T) {
		dir := t.TempDir()
		writeFastAPIFixture(t, dir)

		rec := &pathRecorder{}
		srv := httptest.NewServer(rec.handler())
		defer srv.Close()

		canonDir, err := canonical(dir)
		if err != nil {
			t.Fatal(err)
		}
		sockets := fakeSource{listeners: []Listener{{Port: serverPort(t, srv), PID: 1, Cwd: canonDir}}}

		if _, _, err := Discover(context.Background(), dir, srv.Client(), sockets); err != nil {
			t.Fatalf("Discover: %v", err)
		}

		openapi := rec.indexOf("/openapi.json")
		aspnet := rec.indexOf("/openapi/v1.json")
		if openapi < 0 || aspnet < 0 {
			t.Fatalf("paths tried = %v, want both /openapi.json and /openapi/v1.json", rec.paths)
		}
		if openapi > aspnet {
			t.Errorf("paths tried = %v, want /openapi.json before /openapi/v1.json for a detected FastAPI project", rec.paths)
		}
	})

	t.Run("aspnet tries openapi v1 json first", func(t *testing.T) {
		dir := t.TempDir()
		writeAspNetFixture(t, dir, "")

		rec := &pathRecorder{}
		srv := httptest.NewServer(rec.handler())
		defer srv.Close()

		canonDir, err := canonical(dir)
		if err != nil {
			t.Fatal(err)
		}
		sockets := fakeSource{listeners: []Listener{{Port: serverPort(t, srv), PID: 1, Cwd: canonDir}}}

		if _, _, err := Discover(context.Background(), dir, srv.Client(), sockets); err != nil {
			t.Fatalf("Discover: %v", err)
		}

		openapi := rec.indexOf("/openapi.json")
		aspnet := rec.indexOf("/openapi/v1.json")
		if openapi < 0 || aspnet < 0 {
			t.Fatalf("paths tried = %v, want both /openapi.json and /openapi/v1.json", rec.paths)
		}
		if aspnet > openapi {
			t.Errorf("paths tried = %v, want /openapi/v1.json before /openapi.json for a detected ASP.NET project", rec.paths)
		}
	})
}

func TestDiscoverForeignProcessExcluded(t *testing.T) {
	dir := t.TempDir()
	writeAspNetFixture(t, dir, "https://127.0.0.1:1") // guaranteed unreachable

	var requests int
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openapi":"3.0.0"}`))
	}))
	defer foreign.Close()

	outside := t.TempDir()
	sockets := fakeSource{listeners: []Listener{{Port: serverPort(t, foreign), PID: 99, Cwd: outside}}}

	targets, _, err := Discover(context.Background(), dir, http.DefaultClient, sockets)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("got %d targets, want 0: %+v", len(targets), targets)
	}
	if requests != 0 {
		t.Errorf("the foreign process received %d requests, want 0: its cwd is outside the repo", requests)
	}
}

func TestDiscoverCertificateRule(t *testing.T) {
	t.Run("accepted on loopback", func(t *testing.T) {
		dir := t.TempDir()
		srv := httptest.NewTLSServer(openAPIHandler("/openapi/v1.json"))
		defer srv.Close()
		writeAspNetFixture(t, dir, srv.URL) // srv.URL hosts on 127.0.0.1

		targets, _, err := Discover(context.Background(), dir, &http.Client{}, fakeSource{})
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if len(targets) != 1 {
			t.Fatalf("got %d targets, want 1 (a self-signed cert on loopback must be accepted): %+v", len(targets), targets)
		}
	})

	t.Run("refused off loopback", func(t *testing.T) {
		dir := t.TempDir()
		srv := newSilentTLSServer(openAPIHandler("/openapi/v1.json"))
		defer srv.Close()

		port := serverPort(t, srv)
		applicationURL := fmt.Sprintf("https://example.internal:%d", port)
		writeAspNetFixture(t, dir, applicationURL)

		client := redirectClient(srv.Listener.Addr().String())
		targets, ledger, err := Discover(context.Background(), dir, client, fakeSource{})
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if len(targets) != 0 {
			t.Fatalf("got %d targets, want 0 (a self-signed cert off loopback must be refused): %+v", len(targets), targets)
		}
		found := false
		for _, a := range ledger.Attempts {
			if a.Rung == RungProbe && strings.Contains(a.Outcome, "certificate rejected") && strings.Contains(a.Outcome, "example.internal") {
				found = true
			}
		}
		if !found {
			t.Errorf("ledger = %+v, want an attempt naming the certificate refusal and the non-loopback host", ledger.Attempts)
		}
	})
}

func TestDiscoverTotalFailure(t *testing.T) {
	dir := t.TempDir()
	targets, ledger, err := Discover(context.Background(), dir, http.DefaultClient, fakeSource{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("got %d targets, want 0: %+v", len(targets), targets)
	}
	if len(ledger.Attempts) != 4 {
		t.Fatalf("got %d ledger attempts, want 4 (one per rung): %+v", len(ledger.Attempts), ledger.Attempts)
	}
	for _, a := range ledger.Attempts {
		if strings.TrimSpace(a.Outcome) == "" {
			t.Errorf("attempt %+v has an empty outcome", a)
		}
	}
}

func TestDiscoverNoFalsePositiveOnBundlerOnly(t *testing.T) {
	// noNetworkClient catches a spurious guess even one that finds no target.
	targets, ledger, err := Discover(context.Background(), testdata("frontend"), noNetworkClient(t), fakeSource{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("got %d targets, want 0 (a plain package.json is not evidence of an API): %+v", len(targets), targets)
	}
	found := false
	for _, a := range ledger.Attempts {
		if a.Rung == RungFramework && a.Outcome == "none detected" {
			found = true
		}
	}
	if !found {
		t.Errorf("ledger = %+v, want a framework attempt saying none detected", ledger.Attempts)
	}
}

func TestDiscoverMonorepoTwoProjects(t *testing.T) {
	root := testdata("monorepo")
	ordersCanon, err := canonical(filepath.Join(root, "orders"))
	if err != nil {
		t.Fatal(err)
	}
	catalogCanon, err := canonical(filepath.Join(root, "catalog"))
	if err != nil {
		t.Fatal(err)
	}

	orders := httptest.NewServer(openAPIHandler("/openapi/v1.json"))
	defer orders.Close()
	catalog := httptest.NewServer(openAPIHandler("/openapi.json"))
	defer catalog.Close()

	sockets := fakeSource{listeners: []Listener{
		{Port: serverPort(t, orders), PID: 1, Cwd: ordersCanon},
		{Port: serverPort(t, catalog), PID: 2, Cwd: catalogCanon},
	}}

	targets, _, err := Discover(context.Background(), root, http.DefaultClient, sockets)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("got %d targets, want 2: %+v", len(targets), targets)
	}

	byTitle := map[string]Target{}
	for _, tg := range targets {
		byTitle[tg.Title] = tg
	}
	wantOrders := fmt.Sprintf("http://localhost:%d", serverPort(t, orders))
	wantCatalog := fmt.Sprintf("http://localhost:%d", serverPort(t, catalog))
	if got := byTitle["orders"].BaseURL; got != wantOrders {
		t.Errorf("orders BaseURL = %q, want %q (its own server, not catalog's)", got, wantOrders)
	}
	if got := byTitle["catalog"].BaseURL; got != wantCatalog {
		t.Errorf("catalog BaseURL = %q, want %q (its own server, not orders')", got, wantCatalog)
	}
}

func TestDiscoverRedirectOffLoopbackIsRefused(t *testing.T) {
	dir := t.TempDir()

	evil := httptest.NewTLSServer(openAPIHandler("/openapi.json"))
	defer evil.Close()

	loopback := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.internal/openapi.json", http.StatusFound)
	}))
	defer loopback.Close()

	writeAspNetFixture(t, dir, loopback.URL)

	client := hostMappingClient(map[string]string{"evil.internal": evil.Listener.Addr().String()})
	targets, ledger, err := Discover(context.Background(), dir, client, fakeSource{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("got %d targets, want 0: a cross-host redirect off loopback must be refused, not adopted", len(targets))
	}
	assertRedirectRefusedNotSilent(t, ledger)
}

// assertRedirectRefusedNotSilent catches the refusal being reported as "nothing listening there".
func assertRedirectRefusedNotSilent(t *testing.T, ledger Ledger) {
	t.Helper()
	found := false
	for _, a := range ledger.Attempts {
		if a.Rung != RungProbe {
			continue
		}
		if strings.Contains(a.Outcome, "nothing listening") {
			t.Errorf("outcome = %q, want it to say the redirect was refused: something did answer", a.Outcome)
		}
		if strings.HasPrefix(a.Outcome, "redirect refused") {
			found = true
		}
	}
	if !found {
		t.Errorf("ledger = %+v, want an attempt saying the redirect was refused", ledger.Attempts)
	}
}

func TestDiscoverPlainHTTPRedirectOffHostIsRefused(t *testing.T) {
	dir := t.TempDir()

	foreign := httptest.NewServer(openAPIHandler("/openapi.json"))
	defer foreign.Close()

	loopback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://foreign.internal/openapi.json", http.StatusFound)
	}))
	defer loopback.Close()

	writeAspNetFixture(t, dir, loopback.URL)

	client := hostMappingClient(map[string]string{"foreign.internal": foreign.Listener.Addr().String()})
	targets, ledger, err := Discover(context.Background(), dir, client, fakeSource{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("got %d targets, want 0: a plain http cross-host redirect must be refused, not adopted", len(targets))
	}
	assertRedirectRefusedNotSilent(t, ledger)
}

func TestDiscoverRedirectSameHostRecordsFinalURL(t *testing.T) {
	dir := t.TempDir()
	mux := http.NewServeMux()
	mux.HandleFunc("/openapi/v1.json", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/v2/openapi.json", http.StatusFound)
	})
	mux.HandleFunc("/v2/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openapi":"3.0.0","info":{"title":"Api","version":"1.0"},"paths":{}}`))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	writeAspNetFixture(t, dir, srv.URL)

	targets, _, err := Discover(context.Background(), dir, &http.Client{}, fakeSource{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("got %d targets, want 1 (a same-host redirect must still be followed): %+v", len(targets), targets)
	}
	want := srv.URL + "/v2/openapi.json"
	if targets[0].SpecURL != want {
		t.Errorf("SpecURL = %q, want %q (the URL actually served, not the one first requested)", targets[0].SpecURL, want)
	}
}

func TestDiscoverPreservesCallerTransport(t *testing.T) {
	dir := t.TempDir()
	srv := newSilentTLSServer(openAPIHandler("/openapi/v1.json"))
	defer srv.Close()
	writeAspNetFixture(t, dir, srv.URL)

	spy := &spyTransport{inner: http.DefaultTransport}
	targets, ledger, err := Discover(context.Background(), dir, &http.Client{Transport: spy}, fakeSource{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("got %d targets, want 0: an unrecognized transport must not be silently swapped for a lenient one", len(targets))
	}
	if spy.calls == 0 {
		t.Errorf("the caller's transport was never used; it must not be dropped when TLS cannot be relaxed")
	}
	// The host really is loopback; the message must not claim otherwise.
	for _, a := range ledger.Attempts {
		if a.Rung == RungProbe && strings.Contains(a.Outcome, "is not loopback") {
			t.Errorf("outcome = %q, want no false loopback claim: relaxation failed for another reason", a.Outcome)
		}
	}
}

func TestDiscoverFrameworkScanErrorDoesNotAbortProbing(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(blocked, 0o755)

	srv := httptest.NewServer(openAPIHandler("/openapi/v1.json"))
	defer srv.Close()

	canonDir, err := canonical(dir)
	if err != nil {
		t.Fatal(err)
	}
	sockets := fakeSource{listeners: []Listener{{Port: serverPort(t, srv), PID: 1, Cwd: canonDir}}}

	targets, ledger, err := Discover(context.Background(), dir, srv.Client(), sockets)
	if err != nil {
		t.Fatalf("Discover: %v, want nil: a rung-3 scan error is a ledger line, not a fatal error", err)
	}
	if len(targets) != 1 {
		t.Fatalf("got %d targets, want 1: rung 4 must still run after a rung 3 scan error: %+v", len(targets), targets)
	}
	found := false
	for _, a := range ledger.Attempts {
		if a.Rung == RungFramework && strings.Contains(a.Outcome, "failed") {
			found = true
		}
	}
	if !found {
		t.Errorf("ledger = %+v, want a framework attempt recording the scan failure", ledger.Attempts)
	}
}

func TestDiscoverListenerScanErrorDoesNotAbortDiscovery(t *testing.T) {
	dir := t.TempDir()
	sockets := fakeSource{err: errors.New("boom")}

	targets, ledger, err := Discover(context.Background(), dir, http.DefaultClient, sockets)
	if err != nil {
		t.Fatalf("Discover: %v, want nil: a listener scan error is a ledger line, not a fatal error", err)
	}
	if len(targets) != 0 {
		t.Fatalf("got %d targets, want 0: %+v", len(targets), targets)
	}
	found := false
	for _, a := range ledger.Attempts {
		if a.Rung == RungProbe && strings.Contains(a.Outcome, "failed") {
			found = true
		}
	}
	if !found {
		t.Errorf("ledger = %+v, want a probe attempt recording the listener scan failure", ledger.Attempts)
	}
}

func TestDiscoverSpecFileSendabilityInvariant(t *testing.T) {
	write := func(t *testing.T, servers string) string {
		t.Helper()
		dir := t.TempDir()
		doc := fmt.Sprintf(`{"openapi":"3.0.0","info":{"title":"Api","version":"1.0"},"servers":[%s],"paths":{}}`, servers)
		if err := os.WriteFile(filepath.Join(dir, "openapi.json"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	assertSendable := func(t *testing.T, got Target) {
		t.Helper()
		if got.BaseURL == "" || got.Env == nil || got.Unsendable != "" {
			t.Errorf("got BaseURL=%q Env=%v Unsendable=%q, want the sendable state", got.BaseURL, got.Env, got.Unsendable)
		}
	}
	assertUnsendable := func(t *testing.T, got Target) {
		t.Helper()
		if got.BaseURL != "" || got.Env != nil || got.Unsendable == "" {
			t.Errorf("got BaseURL=%q Env=%v Unsendable=%q, want the unsendable state", got.BaseURL, got.Env, got.Unsendable)
		}
	}

	t.Run("sendable, prefers loopback over a listed production server", func(t *testing.T) {
		dir := write(t, `{"url":"https://api.acme.com/v1"},{"url":"http://localhost:4000"}`)
		targets, ledger, err := Discover(context.Background(), dir, noNetworkClient(t), fakeSource{})
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if len(targets) != 1 {
			t.Fatalf("got %d targets, want 1: %+v", len(targets), targets)
		}
		assertSendable(t, targets[0])
		if targets[0].BaseURL != "http://localhost:4000" {
			t.Errorf("BaseURL = %q, want the loopback server, not the production one", targets[0].BaseURL)
		}
		found := false
		for _, a := range ledger.Attempts {
			if a.Rung == RungSpecFile && a.Outcome == "adopted server http://localhost:4000" {
				found = true
			}
		}
		if !found {
			t.Errorf("ledger = %+v, want an attempt naming the adopted server", ledger.Attempts)
		}
	})

	t.Run("unsendable, no servers at all", func(t *testing.T) {
		dir := write(t, ``)
		targets, _, err := Discover(context.Background(), dir, noNetworkClient(t), fakeSource{})
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if len(targets) != 1 {
			t.Fatalf("got %d targets, want 1: %+v", len(targets), targets)
		}
		assertUnsendable(t, targets[0])
	})

	t.Run("unsendable, only relative or templated servers", func(t *testing.T) {
		dir := write(t, `{"url":"/api/v1"},{"url":"https://{host}/v1"}`)
		targets, _, err := Discover(context.Background(), dir, noNetworkClient(t), fakeSource{})
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if len(targets) != 1 {
			t.Fatalf("got %d targets, want 1: %+v", len(targets), targets)
		}
		assertUnsendable(t, targets[0])
	})
}

func TestDiscoverBrokenConfigRecordsLedgerBeforeErroring(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".blip.toml"), []byte("not valid toml [[["), 0o644); err != nil {
		t.Fatal(err)
	}

	targets, ledger, err := Discover(context.Background(), dir, noNetworkClient(t), fakeSource{})
	if err == nil {
		t.Fatal("Discover: err = nil, want an error for a malformed .blip.toml")
	}
	if targets != nil {
		t.Errorf("targets = %+v, want nil", targets)
	}
	if len(ledger.Attempts) == 0 {
		t.Fatal("ledger has no attempts, want the failed .blip.toml load recorded before erroring")
	}
	last := ledger.Attempts[len(ledger.Attempts)-1]
	if last.Rung != RungConfig || strings.TrimSpace(last.Outcome) == "" {
		t.Errorf("last attempt = %+v, want a config attempt with a non-empty outcome", last)
	}
}

func TestDiscoverConfigWithNoResolvableEnvRecordsLedgerBeforeErroring(t *testing.T) {
	dir := t.TempDir()
	body := `name = "twoenvs"

[env.a]
base_url = "http://localhost:9"

[env.b]
base_url = "http://localhost:10"
`
	if err := os.WriteFile(filepath.Join(dir, ".blip.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ledger, err := Discover(context.Background(), dir, noNetworkClient(t), fakeSource{})
	if err == nil {
		t.Fatal("Discover: err = nil, want an error: two environments and no default_env is ambiguous")
	}
	last := ledger.Attempts[len(ledger.Attempts)-1]
	if last.Rung != RungConfig || strings.TrimSpace(last.Outcome) == "" {
		t.Errorf("last attempt = %+v, want a config attempt with a non-empty outcome", last)
	}
}
