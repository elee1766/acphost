package acphost_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/gfx-labs/acphost/pkg/acphost"
	"github.com/gfx-labs/acphost/pkg/acphosttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tlsFixture is a host with mutual TLS on 127.0.0.1:0 and a CA the clients
// are issued from.
type tlsFixture struct {
	t       *testing.T
	ca      *testCA
	dir     string // the certificates
	hostDir string
	srv     pki
	host    *hostRef
	logs    *logCapture
	addr    string
	allow   []string
}

type hostRef struct{ stop func() }

func newTLSFixture(t *testing.T, allow ...string) *tlsFixture {
	t.Helper()
	return newTLSFixtureWith(t, "", allow...)
}

// newTLSFixtureWith runs the agent command when it is not empty.
func newTLSFixtureWith(t *testing.T, command string, allow ...string) *tlsFixture {
	t.Helper()
	f := &tlsFixture{t: t, ca: newCA(t, "ca-1"), dir: t.TempDir(), allow: allow}
	crt, key := f.ca.issue(t, leaf{server: true, ips: loopback})
	f.srv = writePKI(t, f.dir, "server", crt, key, f.ca.pem)
	h, logs, info := startListening(t, acphost.Spec{
		LeaseMS: 60_000, Command: command,
		TLS: &acphost.ListenTLS{Cert: f.srv.cert, Key: f.srv.key, ClientCA: f.srv.ca, Allow: allow},
	})
	f.logs, f.addr, f.host, f.hostDir = logs, info.Addr, &hostRef{h.Stop}, h.Dir
	assert.True(t, info.TLS)
	return f
}

// clientFiles writes a client certificate from ca, trusting trust.
func (f *tlsFixture) clientFiles(name string, ca *testCA, l leaf, trust ...*testCA) pki {
	f.t.Helper()
	crt, key := ca.issue(f.t, l)
	var bundle []byte
	for _, c := range trust {
		bundle = append(bundle, c.pem...)
	}
	return writePKI(f.t, f.dir, name, crt, key, bundle)
}

func (f *tlsFixture) dial(p pki) (*session, error) {
	nc, rd, _, err := acphost.ConnectTCP(f.t.Context(), f.addr, acphost.WithClientTLSFiles(p.client()))
	if err != nil {
		return nil, err
	}
	f.t.Cleanup(func() { _ = nc.Close() })
	return newSession(nc, rd), nil
}

func TestTCPTLSAcceptsCASignedClient(t *testing.T) {
	f := newTLSFixture(t)
	c := f.clientFiles("c1", f.ca, leaf{dns: []string{"ctl.example"}}, f.ca)
	s, err := f.dial(c)
	require.NoError(t, err)
	s.prompt(t)
}

// Each refused client gets no welcome, the host logs why with the peer, and
// the controller that was connected stays connected.
func TestTCPTLSRefusals(t *testing.T) {
	f := newTLSFixture(t)
	good := f.clientFiles("good", f.ca, leaf{dns: []string{"ctl.example"}}, f.ca)
	cur, err := f.dial(good)
	require.NoError(t, err)
	cur.prompt(t)

	other := newCA(t, "other")
	refused := map[string]func() error{
		"no certificate": func() error {
			cfg := &tls.Config{RootCAs: poolOf(t, f.ca), MinVersion: tls.VersionTLS13}
			nc, _, _, err := acphost.ConnectTCP(t.Context(), f.addr, acphost.WithClientTLS(cfg))
			if err == nil {
				_ = nc.Close()
			}
			return err
		},
		"other CA": func() error {
			p := f.clientFiles("other", other, leaf{dns: []string{"ctl.example"}}, f.ca)
			_, err := f.dial(p)
			return err
		},
		// Go's client does not send a certificate the server's CA list does
		// not name, so force it: the host's verifier is what refuses.
		"other CA, sent anyway": func() error {
			crt, key := other.issue(t, leaf{dns: []string{"ctl.example"}})
			pair, err := tls.X509KeyPair(crt, key)
			require.NoError(t, err)
			cfg := &tls.Config{
				RootCAs: poolOf(t, f.ca), MinVersion: tls.VersionTLS13,
				GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &pair, nil },
			}
			nc, _, _, err := acphost.ConnectTCP(t.Context(), f.addr, acphost.WithClientTLS(cfg))
			if err == nil {
				_ = nc.Close()
			}
			return err
		},
		"expired": func() error {
			p := f.clientFiles("expired", f.ca, leaf{dns: []string{"ctl.example"}, expired: true}, f.ca)
			_, err := f.dial(p)
			return err
		},
		"plaintext": func() error {
			nc, _, _, err := acphost.ConnectTCP(t.Context(), f.addr)
			if err == nil {
				_ = nc.Close()
			}
			return err
		},
	}
	for name, dial := range refused {
		require.Error(t, dial(), name)
	}
	// The host logs each refusal with the peer address and the reason. The
	// client may see its error before the host has logged, so wait for each.
	for _, reason := range []string{
		"client didn't provide a certificate", "signed by unknown authority", "certificate has expired",
		"does not look like a TLS handshake",
	} {
		f.logs.expect(t, "WARN tcp client refused", "peer=127.0.0.1:", reason)
	}
	cur.prompt(t) // still the controller
}

func poolOf(t *testing.T, cas ...*testCA) *x509.CertPool {
	t.Helper()
	p := x509.NewCertPool()
	for _, c := range cas {
		require.True(t, p.AppendCertsFromPEM(c.pem))
	}
	return p
}

func TestTCPTLSAllow(t *testing.T) {
	f := newTLSFixture(t, "ctl.example", "spiffe://cluster.local/ns/jobshop/sa/controller")

	dnsOK := f.clientFiles("dns", f.ca, leaf{dns: []string{"CTL.example"}}, f.ca)
	s, err := f.dial(dnsOK)
	require.NoError(t, err, "DNS SAN in allow, any case")
	s.prompt(t)

	uriOK := f.clientFiles("uri", f.ca, leaf{uris: []string{"spiffe://cluster.local/ns/jobshop/sa/controller"}}, f.ca)
	s, err = f.dial(uriOK)
	require.NoError(t, err, "URI SAN in allow")
	s.prompt(t)

	dnsNo := f.clientFiles("dnsno", f.ca, leaf{dns: []string{"intruder.example"}}, f.ca)
	_, err = f.dial(dnsNo)
	require.Error(t, err, "DNS SAN not in allow")
	f.logs.expect(t, "tcp client refused", "not in the allow list", "intruder.example")

	uriNo := f.clientFiles("urino", f.ca, leaf{uris: []string{"spiffe://cluster.local/ns/jobshop/sa/other"}}, f.ca)
	_, err = f.dial(uriNo)
	require.Error(t, err, "URI SAN not in allow")
	f.logs.expect(t, "tcp client refused", "sa/other")

	noSAN := f.clientFiles("nosan", f.ca, leaf{}, f.ca)
	_, err = f.dial(noSAN)
	require.Error(t, err, "no SAN at all")
	f.logs.expect(t, "no DNS or URI SAN")
}

// Replacing cert, key and client_ca with files from a new CA takes effect
// for the next connection, with no restart and no effect on a connection
// that is open.
func TestTCPTLSServerRotation(t *testing.T) {
	f := newTLSFixture(t)
	oldCA := f.ca
	// Both clients trust both CAs, so a refusal is the host's.
	newCA2 := newCA(t, "ca-2")
	oldClient := f.clientFiles("old", oldCA, leaf{dns: []string{"ctl.example"}}, oldCA, newCA2)
	open, err := f.dial(oldClient)
	require.NoError(t, err)
	open.prompt(t)

	crt, key := newCA2.issue(t, leaf{server: true, ips: loopback})
	replaceFile(t, f.srv.cert, crt)
	replaceFile(t, f.srv.key, key)
	replaceFile(t, f.srv.ca, newCA2.pem)
	open.prompt(t) // the rotation did not drop it

	newClient := f.clientFiles("new", newCA2, leaf{dns: []string{"ctl.example"}}, oldCA, newCA2)
	s, err := f.dial(newClient)
	require.NoError(t, err, "new CA after rotation")
	s.prompt(t)

	// The old controller was replaced by the new connection (one at a
	// time), not by the rotation: check the rotation alone with a fresh
	// dial from the old CA.
	_, err = f.dial(oldClient)
	require.Error(t, err, "old CA after rotation")
	f.logs.expect(t, "tcp client refused")
	s.prompt(t)

	// The connection open before the rotation was closed only when the
	// new controller replaced it.
	open.readsEOF(t)
}

// The rotation alone does not close an open connection.
func TestTCPTLSRotationKeepsOpenConnection(t *testing.T) {
	f := newTLSFixture(t)
	newCA2 := newCA(t, "ca-2")
	open, err := f.dial(f.clientFiles("old", f.ca, leaf{dns: []string{"ctl.example"}}, f.ca, newCA2))
	require.NoError(t, err)
	open.prompt(t)

	crt, key := newCA2.issue(t, leaf{server: true, ips: loopback})
	replaceFile(t, f.srv.cert, crt)
	replaceFile(t, f.srv.key, key)
	replaceFile(t, f.srv.ca, newCA2.pem)

	// A dial that is refused does not replace the open controller.
	_, err = f.dial(f.clientFiles("old2", f.ca, leaf{dns: []string{"ctl.example"}}, f.ca, newCA2))
	require.Error(t, err)
	f.logs.expect(t, "tcp client refused")
	open.prompt(t)
}

// Unreadable or invalid files keep the last good set and log a warning.
func TestTCPTLSBrokenFilesKeepLastGood(t *testing.T) {
	f := newTLSFixture(t)
	c := f.clientFiles("c1", f.ca, leaf{dns: []string{"ctl.example"}}, f.ca)

	for name, replace := range map[string]func(){
		"garbage": func() {
			replaceFile(t, f.srv.cert, []byte("not a certificate"))
			replaceFile(t, f.srv.key, []byte("not a key"))
			replaceFile(t, f.srv.ca, []byte("not a ca"))
		},
		"missing": func() {
			require.NoError(t, os.Remove(f.srv.cert))
			require.NoError(t, os.Remove(f.srv.key))
			require.NoError(t, os.Remove(f.srv.ca))
		},
	} {
		replace()
		s, err := f.dial(c)
		require.NoError(t, err, name)
		s.prompt(t)
		f.logs.expect(t, "WARN tls files not readable", "last good")
	}
}

// The client reads its files on every dial: a certificate replaced between
// two dials is the one presented by the second.
func TestClientFilesReadOnEachDial(t *testing.T) {
	f := newTLSFixture(t)
	c := f.clientFiles("c", f.ca, leaf{dns: []string{"first.example"}}, f.ca)

	s, err := f.dial(c)
	require.NoError(t, err)
	s.prompt(t)

	// A new certificate under the same paths, from a second CA the host
	// does not trust: the second dial must fail, which it can only do if
	// the new file was read.
	other := newCA(t, "other")
	crt, key := other.issue(t, leaf{dns: []string{"second.example"}})
	replaceFile(t, c.cert, crt)
	replaceFile(t, c.key, key)
	_, err = f.dial(c)
	require.Error(t, err, "second dial presents the replaced certificate")

	// And back to a good one.
	crt, key = f.ca.issue(t, leaf{dns: []string{"third.example"}})
	replaceFile(t, c.cert, crt)
	replaceFile(t, c.key, key)
	s, err = f.dial(c)
	require.NoError(t, err)
	s.prompt(t)
}

// A caller with its own certificate source passes a tls.Config.
func TestConnectTCPWithTLSConfig(t *testing.T) {
	f := newTLSFixture(t)
	c := f.clientFiles("c", f.ca, leaf{dns: []string{"ctl.example"}}, f.ca)
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: poolOf(t, f.ca),
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			cert, err := tls.LoadX509KeyPair(c.cert, c.key)
			return &cert, err
		},
	}
	nc, rd, w, err := acphost.ConnectTCP(t.Context(), f.addr, acphost.WithClientTLS(cfg))
	require.NoError(t, err)
	t.Cleanup(func() { _ = nc.Close() })
	assert.Equal(t, acphost.Protocol, w.Protocol)
	newSession(nc, rd).prompt(t)
}

// Without TLS the listener is plaintext with no authentication, and the
// host says so at start.
func TestTCPPlaintext(t *testing.T) {
	h, logs, info := startListening(t, acphost.Spec{LeaseMS: 60_000})
	_ = h
	assert.False(t, info.TLS)
	logs.expect(t, "WARN tcp listener without tls", "addr="+info.Addr)

	nc, rd, _, err := acphost.ConnectTCP(t.Context(), info.Addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = nc.Close() })
	newSession(nc, rd).prompt(t)
}

func TestTCPUnixAndListenFile(t *testing.T) {
	h, _, info := startListening(t, acphost.Spec{LeaseMS: 60_000})
	host, port, err := net.SplitHostPort(info.Addr)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", host)
	assert.NotEqual(t, "0", port, "the bound port, not the asked one")
	raw, err := os.ReadFile(filepath.Join(h.Dir, acphost.ListenFile))
	require.NoError(t, err)
	assert.JSONEq(t, `{"addr":"`+info.Addr+`","tls":false}`, string(raw))

	// The unix socket is unchanged and works with the listener on.
	nc, rd, _, err := acphost.Connect(t.Context(), h.Dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = nc.Close() })
	newSession(nc, rd).prompt(t)
	st, err := os.Stat(acphost.SocketPath(h.Dir))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())

	h.Stop()
	assert.NoFileExists(t, filepath.Join(h.Dir, acphost.ListenFile))
	assert.NoFileExists(t, acphost.SocketPath(h.Dir))
	_, err = net.Dial("tcp", info.Addr)
	assert.Error(t, err, "TCP listener closed when Main returned")
}

func TestNoListenAddress(t *testing.T) {
	dir := t.TempDir()
	h := acphosttest.Start(t, dir, acphost.Spec{
		Session: "s1", Command: writeAgent(t, dir, "exec sleep 600\n"), Dir: dir, GCRootDir: dir, LeaseMS: 60_000,
	})
	assert.NoFileExists(t, filepath.Join(h.Dir, acphost.ListenFile))
	_, ok := acphost.ReadListen(h.Dir)
	assert.False(t, ok)
}

// One controller at a time across both transports.
func TestTCPUnixReplace(t *testing.T) {
	h, _, info := startListening(t, acphost.Spec{LeaseMS: 60_000})
	tcp := func() *session {
		nc, rd, _, err := acphost.ConnectTCP(t.Context(), info.Addr)
		require.NoError(t, err)
		t.Cleanup(func() { _ = nc.Close() })
		return newSession(nc, rd)
	}
	unixc := func() *session {
		nc, rd, _, err := acphost.Connect(t.Context(), h.Dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = nc.Close() })
		return newSession(nc, rd)
	}
	a := tcp()
	a.prompt(t)
	b := unixc() // replaces the TCP controller
	a.readsEOF(t)
	b.prompt(t)
	c := tcp() // replaces the unix controller
	b.readsEOF(t)
	c.prompt(t)
}

// A TCP client reads EOF when the agent exits and Main returns, and the
// lease counts a TCP controller: once it leaves, the lease runs out.
func TestTCPClosedWhenAgentExits(t *testing.T) {
	dir := t.TempDir()
	h, _, info := startListening(t, acphost.Spec{Command: writeAgent(t, dir, "read x\nexit 0\n"), LeaseMS: 60_000})
	nc, rd, _, err := acphost.ConnectTCP(t.Context(), info.Addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = nc.Close() })
	_, err = nc.Write([]byte(`{"jsonrpc":"2.0","method":"x"}` + "\n"))
	require.NoError(t, err)
	newSession(nc, rd).readsEOF(t)
	<-h.Done()
	require.NoError(t, h.Err())
	assert.NoFileExists(t, filepath.Join(h.Dir, acphost.ListenFile))
}

func TestTCPLeaseExpiresAfterController(t *testing.T) {
	dir := t.TempDir()
	h, _, info := startListening(t, acphost.Spec{Command: writeAgent(t, dir, "exec sleep 600\n"), LeaseMS: 200})
	nc, _, _, err := acphost.ConnectTCP(t.Context(), info.Addr)
	require.NoError(t, err)
	require.NoError(t, nc.Close())
	<-h.Done()
	ex, ok := h.Exit()
	require.True(t, ok)
	assert.Equal(t, "no controller for 200ms", ex.Reason)
}

// hello with Since replays journaled notifications, and an agent request
// still unanswered is handed to a new TCP controller.
func TestTCPReplayAndPendingRequest(t *testing.T) {
	dir := t.TempDir()
	agent := writeAgent(t, dir, `echo '{"jsonrpc":"2.0","method":"session/update","params":{"n":1}}'
echo '{"jsonrpc":"2.0","id":"a1","method":"fs/read_text_file"}'
exec sleep 600
`)
	h, _, info := startListening(t, acphost.Spec{Command: agent, LeaseMS: 60_000})
	// Wait for both frames to be journaled.
	waitSeq(t, h.Dir, 2)

	nc, err := net.Dial("tcp", info.Addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = nc.Close() })
	since := int64(0)
	hello, _ := json.Marshal(acphost.Hello{Protocol: acphost.Protocol, Since: &since})
	_, err = nc.Write(append(hello, '\n'))
	require.NoError(t, err)
	rd := bufio.NewReader(nc)
	var w acphost.Welcome
	line, err := rd.ReadBytes('\n')
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(line, &w))
	assert.Equal(t, int64(2), w.Seq)
	var got []string
	for range 2 {
		line, err = rd.ReadBytes('\n')
		require.NoError(t, err)
		var f struct {
			Method string `json:"method"`
		}
		require.NoError(t, json.Unmarshal(line, &f))
		got = append(got, f.Method)
	}
	assert.ElementsMatch(t, []string{"session/update", "fs/read_text_file"}, got)
}

// A prompt sent over TCP whose controller left before the answer is
// reported to the next controller as turn_end.
func TestTCPTurnEnd(t *testing.T) {
	dir := t.TempDir()
	agent := writeAgent(t, dir, `read p
read go
echo '{"jsonrpc":"2.0","id":"h1","result":{"stopReason":"end_turn"}}'
exec sleep 600
`)
	h, _, info := startListening(t, acphost.Spec{Command: agent, LeaseMS: 60_000})
	nc, _, _, err := acphost.ConnectTCP(t.Context(), info.Addr)
	require.NoError(t, err)
	_, err = nc.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"session/prompt","params":{}}` + "\n"))
	require.NoError(t, err)
	waitSeq(t, h.Dir, 1)
	require.NoError(t, nc.Close())

	nc2, rd2, w, err := acphost.Connect(t.Context(), h.Dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = nc2.Close() })
	_ = w
	// Release the agent; its answer belongs to the earlier connection.
	_, err = nc2.Write([]byte(`{"go":true}` + "\n"))
	require.NoError(t, err)
	line, err := rd2.ReadBytes('\n')
	require.NoError(t, err)
	var f struct {
		Method string          `json:"method"`
		Params acphost.TurnEnd `json:"params"`
	}
	require.NoError(t, json.Unmarshal(line, &f))
	assert.Equal(t, acphost.MethodTurnEnd, f.Method)
	assert.JSONEq(t, `{"stopReason":"end_turn"}`, string(f.Params.Result))
}

// waitSeq returns once the journal in dir holds frame want.
func waitSeq(t *testing.T, dir string, want int64) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := acphost.FollowJournal(ctx, filepath.Join(dir, acphost.JournalFile), 0, func(f acphost.Frame) {
		if f.Seq >= want {
			cancel()
		}
	})
	if err != nil && ctx.Err() == nil {
		t.Fatalf("follow journal: %v", err)
	}
	if t.Context().Err() != nil {
		t.Fatalf("journal did not reach seq %d", want)
	}
}

// The TLS files are the host's: their paths do not reach the agent's
// environment.
func TestTLSPathsNotInAgentEnv(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.out")
	agent := writeAgent(t, dir, "env > "+envFile+"\necho '{\"jsonrpc\":\"2.0\",\"method\":\"up\"}'\nexec sleep 600\n")
	f := newTLSFixtureWith(t, agent)
	_ = f
	waitSeq(t, f.hostDir, 1) // the agent journals "up" after writing its env
	env, err := os.ReadFile(envFile)
	require.NoError(t, err)
	for _, p := range []string{f.srv.cert, f.srv.key, f.srv.ca} {
		assert.NotContains(t, string(env), p)
	}
}
