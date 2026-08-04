// Package request builds and executes HTTP requests against a resolved environment.
package request

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dotnetemmanuel/blip/internal/config"
	"github.com/dotnetemmanuel/blip/internal/output"
)

// MaxRedirects matches the Go client's own default.
const MaxRedirects = 10

// NewClient builds the HTTP client for talking to the API itself.
func NewClient(env *config.Environment, timeout time.Duration) (*http.Client, error) {
	return newClient(env, timeout, env.Insecure)
}

// NewStrictClient builds a client for a host that is not the API's own, such as
// an OAuth2 token endpoint. It always verifies TLS: `insecure` is granted for a
// localhost development certificate, and must not follow a credential to an
// identity provider somewhere else.
func NewStrictClient(env *config.Environment, timeout time.Duration) (*http.Client, error) {
	return newClient(env, timeout, false)
}

func newClient(env *config.Environment, timeout time.Duration, insecure bool) (*http.Client, error) {
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecure,
	}

	if env.ClientCert != "" {
		cert, err := tls.LoadX509KeyPair(env.ClientCert, env.ClientKey)
		if err != nil {
			return nil, output.WithCode(
				fmt.Errorf("env.%s client certificate: %w", env.Name, err), output.ExitConfig)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, output.WithCode(fmt.Errorf("unexpected default transport"), output.ExitInternal)
	}
	t := transport.Clone()
	t.TLSClientConfig = tlsConfig

	if timeout <= 0 {
		timeout = env.Timeout
	}
	return &http.Client{Transport: t, Timeout: timeout, CheckRedirect: CheckRedirect}, nil
}

// ErrRedirectRefused marks a redirect blip declined to follow, so it surfaces as
// a safety refusal rather than as a network failure.
var ErrRedirectRefused = errors.New("redirect refused")

// CheckRedirect keeps credentials and a relaxed TLS grant off the origin host.
func CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= MaxRedirects {
		return fmt.Errorf("stopped after %d redirects", MaxRedirects)
	}

	origin := via[0].URL
	switch {
	case !strings.EqualFold(req.URL.Host, origin.Host):
		return fmt.Errorf("%w: from %s to %s, credentials would leave the configured host",
			ErrRedirectRefused, origin.Host, req.URL.Host)
	case origin.Scheme == "https" && req.URL.Scheme != "https":
		return fmt.Errorf("%w: from https to %s, credentials would leave TLS",
			ErrRedirectRefused, req.URL.Scheme)
	}
	return nil
}
