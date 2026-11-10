package acphost

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// ListenFile is written to the session directory once the TCP listener
// (Spec.Listen) is up, before the agent starts, and removed when Main
// returns. It holds ListenInfo, so a caller that asked for port 0 learns
// the port.
const ListenFile = "listen.json"

// ListenTLS configures mutual TLS on the TCP listener (Spec.TLS). The host
// reads the files itself, outside the agent's sandbox, and never mounts
// them into it or puts their paths in the agent's environment.
type ListenTLS struct {
	// Cert and Key are the host's server certificate and key (PEM).
	Cert string `json:"cert"`
	Key  string `json:"key"`
	// ClientCA is the PEM bundle client certificates must chain to.
	ClientCA string `json:"client_ca"`
	// Allow, if not empty, lists the client identities that may connect.
	// An entry matches a DNS SAN (case-insensitive) or a URI SAN exactly,
	// so a SPIFFE ID is an entry as is. Empty means any certificate the
	// CA signed.
	Allow []string `json:"allow,omitempty"`
}

// ListenInfo is the content of ListenFile.
type ListenInfo struct {
	Addr string `json:"addr"` // bound host:port
	TLS  bool   `json:"tls"`  // mutual TLS, else plaintext with no authentication
}

// ReadListen loads listen.json from dir. It reports false when the host
// has no TCP listener, is not up yet, or has exited.
func ReadListen(dir string) (ListenInfo, bool) {
	var l ListenInfo
	b, err := os.ReadFile(filepath.Join(dir, ListenFile))
	if err != nil || json.Unmarshal(b, &l) != nil {
		return l, false
	}
	return l, true
}

func writeListen(dir string, l ListenInfo) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, ListenFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// WithListening calls f once the TCP listener is up and listen.json is
// written, before the agent starts. It is not called without Spec.Listen.
func WithListening(f func(ListenInfo)) Option {
	return func(o *options) { o.listening = f }
}

// checkListenSpec checks Spec.Listen and Spec.TLS without touching the
// host: the files are checked by checkListenFiles.
func checkListenSpec(s Spec) error {
	if s.Listen == "" {
		if s.TLS != nil {
			return Permanentf("spec tls needs spec listen: there is no TCP listener to apply it to")
		}
		return nil
	}
	host, port, err := net.SplitHostPort(s.Listen)
	if err != nil || host == "" {
		return Permanentf("listen %q: want host:port with an explicit host (0.0.0.0:PORT or [::]:PORT for every interface)", s.Listen)
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return Permanentf("listen %q: port %q is not a number from 0 to 65535", s.Listen, port)
	}
	t := s.TLS
	if t == nil {
		return nil
	}
	for _, f := range []struct{ name, val string }{{"cert", t.Cert}, {"key", t.Key}, {"client_ca", t.ClientCA}} {
		if f.val == "" {
			return Permanentf("spec tls: %s is required", f.name)
		}
	}
	if slices.Contains(t.Allow, "") {
		return Permanentf("spec tls: allow has an empty entry")
	}
	return nil
}

// checkListenFiles checks that the TLS files load.
func checkListenFiles(s Spec) error {
	if s.TLS == nil {
		return nil
	}
	if _, err := loadTLSSet(*s.TLS); err != nil {
		return Permanentf("spec tls: %w", err)
	}
	return nil
}

// tlsSet is what the host reads from the TLS files.
type tlsSet struct {
	cert tls.Certificate
	pool *x509.CertPool
}

func loadTLSSet(t ListenTLS) (*tlsSet, error) {
	cert, err := tls.LoadX509KeyPair(t.Cert, t.Key)
	if err != nil {
		return nil, fmt.Errorf("server certificate: %w", err)
	}
	pem, err := os.ReadFile(t.ClientCA)
	if err != nil {
		return nil, fmt.Errorf("client_ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("client_ca %s: no PEM certificate", t.ClientCA)
	}
	return &tlsSet{cert: cert, pool: pool}, nil
}

// tlsReloader serves the TLS settings for the listener. It reads the
// files again for each new connection, so replaced files apply without a
// restart and without a watcher; controller connections are rare. When
// the files cannot be read at that moment it keeps the last good set.
type tlsReloader struct {
	spec ListenTLS
	log  *slog.Logger

	mu   sync.Mutex
	last *tlsSet
}

func newTLSReloader(t ListenTLS, log *slog.Logger) (*tlsReloader, error) {
	set, err := loadTLSSet(t)
	if err != nil {
		return nil, err
	}
	return &tlsReloader{spec: t, log: log, last: set}, nil
}

// serverConfig is the listener's tls.Config. TLS 1.3 only, and every
// handshake gets its settings from config.
func (r *tlsReloader) serverConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS13, GetConfigForClient: r.config}
}

func (r *tlsReloader) config(*tls.ClientHelloInfo) (*tls.Config, error) {
	set, err := loadTLSSet(r.spec)
	r.mu.Lock()
	if err == nil {
		r.last = set
	} else {
		set = r.last
	}
	r.mu.Unlock()
	if err != nil {
		r.log.Warn("tls files not readable, keeping the last good set", "err", err)
	}
	c := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{set.cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    set.pool,
	}
	if allow := r.spec.Allow; len(allow) > 0 {
		// Runs after the chain is verified, so verifiedChains is not empty.
		c.VerifyPeerCertificate = func(_ [][]byte, chains [][]*x509.Certificate) error {
			if len(chains) == 0 || len(chains[0]) == 0 || !identityAllowed(chains[0][0], allow) {
				return errors.New("client certificate is not in the allow list: " + identities(chains))
			}
			return nil
		}
	}
	return c, nil
}

// identityAllowed reports whether a DNS or URI SAN of cert is in allow.
func identityAllowed(cert *x509.Certificate, allow []string) bool {
	for _, d := range cert.DNSNames {
		if slices.ContainsFunc(allow, func(a string) bool { return strings.EqualFold(a, d) }) {
			return true
		}
	}
	for _, u := range cert.URIs {
		if slices.Contains(allow, u.String()) {
			return true
		}
	}
	return false
}

// identities lists the DNS and URI SANs of the leaf for the refusal log.
func identities(chains [][]*x509.Certificate) string {
	if len(chains) == 0 || len(chains[0]) == 0 {
		return "no certificate"
	}
	leaf := chains[0][0]
	ids := slices.Clone(leaf.DNSNames)
	for _, u := range leaf.URIs {
		ids = append(ids, u.String())
	}
	if len(ids) == 0 {
		return "no DNS or URI SAN"
	}
	return strings.Join(ids, ", ")
}

// Dead peers: a TCP peer can vanish without closing (machine gone, route
// cut). Keepalive probes catch it while the connection is idle (30s idle,
// then 3 probes 10s apart), and TCP_USER_TIMEOUT while data is unacked
// (a write the peer never acknowledged). Either way the host sees an error
// on the connection about deadPeerBound after the peer is lost, so
// dropConn clears h.conn and the lease can run out.
const deadPeerBound = 60 * time.Second

// listenTCP listens on addr with the dead-peer settings on every accepted
// connection.
func listenTCP(addr string, log *slog.Logger) (net.Listener, error) {
	lc := net.ListenConfig{KeepAliveConfig: net.KeepAliveConfig{
		Enable: true, Idle: 30 * time.Second, Interval: 10 * time.Second, Count: 3,
	}}
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, err
	}
	return &userTimeoutListener{Listener: ln, log: log}, nil
}

// userTimeoutListener sets TCP_USER_TIMEOUT on accepted connections.
type userTimeoutListener struct {
	net.Listener
	log *slog.Logger
}

func (l *userTimeoutListener) Accept() (net.Conn, error) {
	nc, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tc, ok := nc.(*net.TCPConn); ok {
		if err := setUserTimeout(tc, deadPeerBound); err != nil {
			l.log.Warn("tcp user timeout not set, a dead peer is found by keepalive only", "err", err)
		}
	}
	return nc, nil
}

func setUserTimeout(tc *net.TCPConn, d time.Duration) error {
	rc, err := tc.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := rc.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, int(d.Milliseconds()))
	}); err != nil {
		return err
	}
	return serr
}
