package request

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotnetemmanuel/blip/internal/config"
	"github.com/dotnetemmanuel/blip/internal/output"
)

// certificate is a self-signed CA that also signs leaf certificates, which is
// enough to stand up both sides of an mTLS handshake in a test.
type certificate struct {
	cert     *x509.Certificate
	key      *ecdsa.PrivateKey
	certPEM  []byte
	keyPEM   []byte
	tlsCert  tls.Certificate
	certPool *x509.CertPool
}

func newCA(t *testing.T) *certificate {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "blip test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	return sign(t, template, nil)
}

func (ca *certificate) issue(t *testing.T, commonName string, server bool) *certificate {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	if server {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		template.DNSNames = []string{"localhost"}
	}
	return sign(t, template, ca)
}

func sign(t *testing.T, template *x509.Certificate, parent *certificate) *certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	signer := template
	signerKey := key
	if parent != nil {
		signer = parent.cert
		signerKey = parent.key
	}

	der, err := x509.CreateCertificate(rand.Reader, template, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)

	return &certificate{cert: cert, key: key, certPEM: certPEM, keyPEM: keyPEM, tlsCert: pair, certPool: pool}
}

func (c *certificate) writeTo(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	certPath = filepath.Join(dir, "client.pem")
	keyPath = filepath.Join(dir, "client.key")
	if err := os.WriteFile(certPath, c.certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, c.keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func mTLSServer(t *testing.T, ca *certificate, server *certificate) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := ""
		if len(r.TLS.PeerCertificates) > 0 {
			name = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		_, _ = w.Write([]byte(`{"client":"` + name + `"}`))
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{server.tlsCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.certPool,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func environment(t *testing.T, rawURL string) *config.Environment {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return &config.Environment{Name: "staging", BaseURL: u, Timeout: 5 * time.Second}
}

func TestClientPresentsAClientCertificate(t *testing.T) {
	ca := newCA(t)
	srv := mTLSServer(t, ca, ca.issue(t, "server", true))

	client := ca.issue(t, "blip-client", false)
	certPath, keyPath := client.writeTo(t, t.TempDir())

	env := environment(t, srv.URL)
	env.ClientCert = certPath
	env.ClientKey = keyPath
	// The test CA is not in the system pool; trusting it is not what is under test.
	env.Insecure = true

	httpClient, err := NewClient(env, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	req := &Request{Method: "GET", Path: "/whoami"}
	httpReq, err := req.HTTPRequest(t.Context(), env.BaseURL)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := Do(httpClient, httpReq)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if want := `{"client":"blip-client"}`; string(resp.Body) != want {
		t.Errorf("body = %q, want %q", resp.Body, want)
	}
}

func TestWithoutAClientCertificateAnMTLSServerRefuses(t *testing.T) {
	ca := newCA(t)
	srv := mTLSServer(t, ca, ca.issue(t, "server", true))

	env := environment(t, srv.URL)
	env.Insecure = true

	httpClient, err := NewClient(env, 0)
	if err != nil {
		t.Fatal(err)
	}
	httpReq, err := (&Request{Method: "GET", Path: "/whoami"}).HTTPRequest(t.Context(), env.BaseURL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Do(httpClient, httpReq)
	if err == nil {
		t.Fatal("Do succeeded without a client certificate")
	}
	if got := output.ExitCodeFor(err); got != output.ExitTransport {
		t.Errorf("exit code = %d, want %d", got, output.ExitTransport)
	}
}

func TestClientRejectsAnUnreadableCertificate(t *testing.T) {
	env := environment(t, "https://api.test")
	env.ClientCert = filepath.Join(t.TempDir(), "missing.pem")
	env.ClientKey = filepath.Join(t.TempDir(), "missing.key")

	_, err := NewClient(env, 0)

	if err == nil {
		t.Fatal("NewClient succeeded with a missing certificate")
	}
	if got := output.ExitCodeFor(err); got != output.ExitConfig {
		t.Errorf("exit code = %d, want %d", got, output.ExitConfig)
	}
	if !strings.Contains(err.Error(), "client certificate") {
		t.Errorf("err = %q, want it to name the problem", err)
	}
}

func TestClientTimeoutPrefersTheFlag(t *testing.T) {
	env := environment(t, "https://api.test")

	client, err := NewClient(env, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if client.Timeout != 2*time.Second {
		t.Errorf("Timeout = %v, want the flag to win", client.Timeout)
	}

	client, err = NewClient(env, 0)
	if err != nil {
		t.Fatal(err)
	}
	if client.Timeout != env.Timeout {
		t.Errorf("Timeout = %v, want the environment's %v", client.Timeout, env.Timeout)
	}
}
