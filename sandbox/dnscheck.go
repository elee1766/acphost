package sandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/elee1766/acphost/internal/xerr"
)

// dnsCheckEnv marks a re-exec of the host binary that resolves a name
// with the nameserver from a resolv.conf and exits 0 on success. See
// CheckDNS.
const dnsCheckEnv = "_ACPHOST_DNS_CHECK"

func init() {
	if os.Getenv(dnsCheckEnv) == "" {
		return
	}
	// Never returns.
	dnsCheckChild()
}

// CheckDNS resolves name inside a pasta namespace set up as s
// would set it up for an agent (same DNS forward and deny rules), using
// the first nameserver in s.ResolvConf, or the host's /etc/resolv.conf
// when ResolvConf is empty. It returns nil for any network but pasta.
//
// It spawns pasta, so call it once at load time, not per session. A
// failure is a PermanentError naming the sandbox.dns and
// sandbox.resolv_conf settings: on a host whose resolver is a loopback
// stub (systemd-resolved's 127.0.0.53) the namespace cannot reach it
// without a forward. See docs/notes/124-sandbox-dns.md.
func CheckDNS(s Sandbox, name string) error {
	if s.Network != "pasta" {
		return nil
	}
	prefix, _, err := PastaPrefix(s)
	if err != nil {
		return &xerr.PermanentError{Err: err}
	}
	conf := s.ResolvConf
	if conf == "" {
		conf = "/etc/resolv.conf"
	}
	ns, err := firstNameserver(conf)
	if err != nil {
		return xerr.Permanentf("sandbox dns: %v; set sandbox.resolv_conf to a file naming sandbox.dns.forward", err)
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("sandbox dns: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// pasta execs the command itself, so /proc/self/exe would be pasta.
	argv := append(slices.Clone(prefix), self)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), dnsCheckEnv+"="+ns+" "+name)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = err.Error()
	}
	return xerr.Permanentf("sandbox dns: cannot resolve %s via nameserver %s from %s inside pasta: %s; "+
		"if the host resolver is on loopback (such as 127.0.0.53), set sandbox.dns "+
		"(forward: an address inside the namespace, host: the host resolver) and "+
		"sandbox.resolv_conf to a file naming the forward address", name, ns, conf, msg)
}

// firstNameserver returns the first nameserver line of a resolv.conf.
func firstNameserver(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) >= 2 && fs[0] == "nameserver" {
			return fs[1], nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", errors.New(path + " has no nameserver line")
}

func dnsCheckChild() {
	ns, name, _ := strings.Cut(os.Getenv(dnsCheckEnv), " ")
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, net.JoinHostPort(ns, "53"))
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, err := r.LookupHost(ctx, name)
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
