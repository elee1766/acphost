package reexec

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"
)

// dnsCheckChild resolves name with nameserver ns and exits 0 on
// success. pasta execs its command by path, so argv[0] is the binary
// and the helper is named by argv[1] (sandbox.CheckDNS). It reads
// nothing else and runs nothing, so a stray match only does a lookup.
func dnsCheckChild(ns, name string) {
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
