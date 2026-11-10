package acphost_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elee1766/acphost/pkg/acphost"
	"github.com/elee1766/acphost/pkg/acphosttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testCA is a real CA made with crypto/x509.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newCA(t *testing.T, name string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: serial(t), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func serial(t *testing.T) *big.Int {
	t.Helper()
	n, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	require.NoError(t, err)
	return n
}

// leaf describes a certificate to issue.
type leaf struct {
	dns     []string
	uris    []string
	ips     []net.IP
	server  bool
	expired bool
}

// issue returns the certificate and key as PEM.
func (ca *testCA) issue(t *testing.T, l leaf) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	var uris []*url.URL
	for _, u := range l.uris {
		p, err := url.Parse(u)
		require.NoError(t, err)
		uris = append(uris, p)
	}
	usage := x509.ExtKeyUsageClientAuth
	if l.server {
		usage = x509.ExtKeyUsageServerAuth
	}
	nb, na := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	if l.expired {
		nb, na = time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(t), Subject: pkix.Name{CommonName: "test"},
		NotBefore: nb, NotAfter: na, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{usage}, DNSNames: l.dns, URIs: uris, IPAddresses: l.ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)
	kb, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb})
}

// replaceFile swaps path's content through a rename, as cert-manager and
// SPIRE do, so a reader never sees a partial file.
func replaceFile(t *testing.T, path string, b []byte) {
	t.Helper()
	tmp := path + ".new"
	require.NoError(t, os.WriteFile(tmp, b, 0o600))
	require.NoError(t, os.Rename(tmp, path))
}

var loopback = []net.IP{net.ParseIP("127.0.0.1")}

// pki is the file set of one TLS side: certificate, key and the CA bundle.
type pki struct {
	cert, key, ca string
}

func writePKI(t *testing.T, dir, name string, certPEM, keyPEM, caPEM []byte) pki {
	t.Helper()
	p := pki{filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key"), filepath.Join(dir, name+"-ca.crt")}
	replaceFile(t, p.cert, certPEM)
	replaceFile(t, p.key, keyPEM)
	replaceFile(t, p.ca, caPEM)
	return p
}

func (p pki) client() acphost.ClientTLSFiles {
	return acphost.ClientTLSFiles{Cert: p.cert, Key: p.key, CA: p.ca}
}

// logCapture is a slog handler that records messages, so a test waits for
// one with an event and not a sleep.
type logCapture struct {
	mu    sync.Mutex
	lines []string
	ch    chan string
}

func newLogCapture() *logCapture { return &logCapture{ch: make(chan string, 1024)} }

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler       { return c }
func (c *logCapture) WithGroup(string) slog.Handler            { return c }
func (c *logCapture) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Level.String() + " " + r.Message)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	})
	c.mu.Lock()
	c.lines = append(c.lines, b.String())
	c.mu.Unlock()
	select {
	case c.ch <- b.String():
	default:
	}
	return nil
}

func (c *logCapture) all() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

// expect waits for a log line containing every part. The timeout is only a
// failsafe for a test that would hang.
func (c *logCapture) expect(t *testing.T, parts ...string) string {
	t.Helper()
	timeout := time.After(30 * time.Second)
	for {
		c.mu.Lock()
		for _, l := range c.lines {
			if containsAll(l, parts) {
				c.mu.Unlock()
				return l
			}
		}
		c.mu.Unlock()
		select {
		case <-c.ch:
		case <-timeout:
			t.Fatalf("no log line with %q in:\n%s", parts, c.all())
		}
	}
}

func containsAll(s string, parts []string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

// echoAgent answers every request line with an end_turn result under the
// id the host assigned (h1, h2, ...). Send it requests only.
const echoAgent = `n=0
while read -r l; do
  n=$((n+1))
  echo "{\"jsonrpc\":\"2.0\",\"id\":\"h$n\",\"result\":{\"stopReason\":\"end_turn\"}}"
done
`

func writeAgent(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o700))
	return p
}

// startListening starts a host with spec.Listen on 127.0.0.1:0 and returns
// it with its log and the bound address.
func startListening(t *testing.T, spec acphost.Spec) (*acphosttest.Host, *logCapture, acphost.ListenInfo) {
	t.Helper()
	dir := t.TempDir()
	if spec.Command == "" {
		spec.Command = writeAgent(t, dir, echoAgent)
	}
	spec.Session, spec.Dir, spec.GCRootDir = "s1", dir, dir
	spec.Listen = "127.0.0.1:0"
	logs := newLogCapture()
	h := acphosttest.Start(t, dir, spec, acphost.WithLogger(slog.New(logs)))
	info, ok := acphost.ReadListen(dir)
	require.True(t, ok, "listen.json once the socket is up")
	return h, logs, info
}

// session is a connected controller.
type session struct {
	nc   net.Conn
	rd   *bufio.Reader
	next int
}

func newSession(nc net.Conn, rd *bufio.Reader) *session { return &session{nc: nc, rd: rd} }

// prompt sends one request and checks the agent's answer.
func (s *session) prompt(t *testing.T) {
	t.Helper()
	s.next++
	_, err := fmt.Fprintf(s.nc, `{"jsonrpc":"2.0","id":%d,"method":"session/prompt","params":{}}`+"\n", s.next)
	require.NoError(t, err)
	line, err := s.rd.ReadBytes('\n')
	require.NoError(t, err)
	var r struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
	}
	require.NoError(t, json.Unmarshal(line, &r), string(line))
	assert.Equal(t, s.next, r.ID)
	assert.JSONEq(t, `{"stopReason":"end_turn"}`, string(r.Result))
}

// readsEOF reports that the host closed the connection.
func (s *session) readsEOF(t *testing.T) {
	t.Helper()
	_, err := s.rd.ReadBytes('\n')
	require.Error(t, err)
}
