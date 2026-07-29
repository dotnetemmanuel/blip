// Package request builds and executes HTTP requests against a resolved environment.
package request

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/dotnetemmanuel/blip/internal/config"
	"github.com/dotnetemmanuel/blip/internal/output"
)

// NewClient builds the HTTP client for an environment, applying its timeout, its
// TLS posture and any client certificate.
func NewClient(env *config.Environment, timeout time.Duration) (*http.Client, error) {
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: env.Insecure,
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
	return &http.Client{Transport: t, Timeout: timeout}, nil
}
