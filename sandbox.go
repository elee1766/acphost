package acphost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Sandbox describes how to confine the agent process. Only the
// "bubblewrap" backend exists.
type Sandbox struct {
	// Backend names the sandbox tool. Empty or "bubblewrap".
	Backend string `json:"backend,omitempty"`
	// Bwrap is the bwrap binary. Empty means look it up on PATH.
	Bwrap string `json:"bwrap,omitempty"`
	// RO lists read-only bind mounts.
	RO []Mount `json:"ro,omitempty"`
	// RW lists read-write bind mounts.
	RW []Mount `json:"rw,omitempty"`
	// Tmpfs lists empty tmpfs mounts. Only Dst is used.
	Tmpfs []Mount `json:"tmpfs,omitempty"`
	// Hide lists paths covered by an empty tmpfs after all binds.
	Hide []string `json:"hide,omitempty"`
	// EnvKeep names host env variables passed into the sandbox. All
	// others are cleared.
	EnvKeep []string `json:"env_keep,omitempty"`
	// Network is "host" (default), "none", or "pasta" (a private
	// network namespace with user-mode networking from passt's pasta).
	Network string `json:"network,omitempty"`
	// Deny lists CIDRs the agent cannot reach. Only valid with "pasta".
	Deny []string `json:"deny,omitempty"`
	// Pasta is the pasta binary. Empty means look it up on PATH.
	Pasta string `json:"pasta,omitempty"`
	// ExtraArgs are passed to bwrap after everything else, before "--".
	ExtraArgs []string `json:"extra_args,omitempty"`
}

// Mount is one mount in a Sandbox. In JSON it is either a path string
// (source and destination equal) or an object.
type Mount struct {
	Src string `json:"src"`
	// Dst defaults to Src.
	Dst string `json:"dst,omitempty"`
	// SkipMissing skips an RO mount whose source does not exist instead
	// of failing. It has no effect on RW mounts.
	SkipMissing bool `json:"skip_missing,omitempty"`
}

// UnmarshalJSON accepts a path string or a {"src","dst"} object.
func (m *Mount) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*m = Mount{Src: s}
		return nil
	}
	type plain Mount
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*m = Mount(p)
	return nil
}

func (m Mount) dst() string {
	if m.Dst != "" {
		return filepath.Clean(m.Dst)
	}
	return filepath.Clean(m.Src)
}

func pathDepth(p string) int {
	if p == "/" {
		return 0
	}
	return strings.Count(p, "/")
}

func within(p, root string) bool {
	if root == "/" {
		return strings.HasPrefix(p, "/")
	}
	return p == root || strings.HasPrefix(p, root+"/")
}

// SandboxArgs returns the full bwrap argv, starting with the bwrap path,
// that runs cmd with args in dir. It reads only the filesystem (to check
// sources) and the environment (for EnvKeep and PATH); the same inputs
// give the same output. dir must lie inside an RW mount's destination.
func SandboxArgs(s Sandbox, cmd string, args []string, dir string) ([]string, error) {
	argv, _, err := sandboxArgs(s, cmd, args, dir)
	return argv, err
}

// sandboxArgs is SandboxArgs that also reports the network setup the
// argv applies, for isolation.json.
func sandboxArgs(s Sandbox, cmd string, args []string, dir string) ([]string, *NetworkApplied, error) {
	fail := func(err error) ([]string, *NetworkApplied, error) { return nil, nil, err }
	if s.Backend != "" && s.Backend != "bubblewrap" {
		return fail(fmt.Errorf("sandbox: unknown backend %q", s.Backend))
	}
	var unshareNet, pasta bool
	switch s.Network {
	case "", "host":
	case "none":
		unshareNet = true
	case "pasta":
		pasta = true
	default:
		return fail(fmt.Errorf("sandbox: unknown network %q", s.Network))
	}
	if len(s.Deny) > 0 && !pasta {
		return fail(fmt.Errorf("sandbox: deny needs network pasta, not %q", s.Network))
	}
	net := &NetworkApplied{Mode: s.Network}
	if net.Mode == "" {
		net.Mode = "host"
	}
	var prefix []string
	if pasta {
		var err error
		if prefix, net.Deny, err = pastaPrefix(s); err != nil {
			return fail(err)
		}
	}
	bwrap := s.Bwrap
	if bwrap == "" {
		p, err := lookPath("bwrap")
		if err != nil {
			return fail(fmt.Errorf("sandbox: %w", err))
		}
		bwrap = p
	}

	type bind struct {
		flag, src, dst string
	}
	var binds []bind
	for _, m := range s.RO {
		if _, err := os.Stat(m.Src); err != nil {
			if errors.Is(err, os.ErrNotExist) && m.SkipMissing {
				continue
			}
			return fail(fmt.Errorf("sandbox: ro %s: %w", m.Src, err))
		}
		binds = append(binds, bind{"--ro-bind", m.Src, m.dst()})
	}
	for _, m := range s.RW {
		if _, err := os.Stat(m.Src); err != nil {
			return fail(fmt.Errorf("sandbox: rw %s: %w", m.Src, err))
		}
		binds = append(binds, bind{"--bind", m.Src, m.dst()})
	}
	for _, m := range s.Tmpfs {
		binds = append(binds, bind{"--tmpfs", "", m.dst()})
	}
	// Parents first. Stable, so at equal depth the input order holds and
	// a later mount at the same path wins.
	sort.SliceStable(binds, func(i, j int) bool {
		return pathDepth(binds[i].dst) < pathDepth(binds[j].dst)
	})

	cdir := filepath.Clean(dir)
	inRW := false
	for _, m := range s.RW {
		if within(cdir, m.dst()) {
			inRW = true
			break
		}
	}
	if !inRW {
		return fail(fmt.Errorf("sandbox: dir %s is not inside an rw mount", dir))
	}

	argv := []string{
		bwrap,
		"--die-with-parent", "--unshare-pid", "--new-session",
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp",
	}
	if unshareNet {
		argv = append(argv, "--unshare-net")
	}
	if pasta {
		// bwrap runs as root in pasta's user namespace. Without this the
		// agent keeps CAP_NET_ADMIN there and could delete the deny rules.
		argv = append(argv, "--cap-drop", "ALL")
	}
	for _, b := range binds {
		if b.flag == "--tmpfs" {
			argv = append(argv, b.flag, b.dst)
		} else {
			argv = append(argv, b.flag, b.src, b.dst)
		}
	}
	for _, h := range s.Hide {
		argv = append(argv, "--tmpfs", filepath.Clean(h))
	}
	// The agent sees only the env it was given, so host secrets do not
	// leak in by default.
	argv = append(argv, "--clearenv")
	for _, k := range s.EnvKeep {
		if v, ok := os.LookupEnv(k); ok {
			argv = append(argv, "--setenv", k, v)
		}
	}
	argv = append(argv, "--chdir", cdir)
	argv = append(argv, s.ExtraArgs...)
	argv = append(argv, "--", cmd)
	return append(prefix, append(argv, args...)...), net, nil
}

// pastaPrefix returns the argv that runs bwrap under pasta. pasta creates
// the network namespace, configures it, and only then starts its command,
// so the agent never sees a namespace without its interface or rules.
// With Deny, a shell loads nftables rules before exec'ing bwrap; exit 125
// means the rules failed to load.
// It also returns the normalized deny CIDRs the rules reject.
func pastaPrefix(s Sandbox) ([]string, []string, error) {
	deny, cidrs, err := denyRules(s.Deny)
	if err != nil {
		return nil, nil, err
	}
	p := s.Pasta
	if p == "" {
		if p, err = lookPath("pasta"); err != nil {
			return nil, nil, fmt.Errorf("sandbox: %w", err)
		}
	}
	// Foreground, quiet, and no inbound port forwards: the agent only
	// makes outbound connections.
	argv := []string{p, "--config-net", "--foreground", "--quiet", "-t", "none", "-u", "none", "-T", "none", "-U", "none", "--"}
	if deny == "" {
		return argv, nil, nil
	}
	sh, err := lookPath("sh")
	if err != nil {
		return nil, nil, fmt.Errorf("sandbox: %w", err)
	}
	nft, err := lookPath("nft")
	if err != nil {
		return nil, nil, fmt.Errorf("sandbox: deny: %w", err)
	}
	return append(argv, sh, "-c", `"$1" "$2" || exit 125; shift 2; exec "$@"`, "acphost-net", nft, deny), cidrs, nil
}

// denyRules validates CIDRs and returns an nft ruleset rejecting them on
// output, or "" for none, and the masked CIDRs (v4 then v6).
func denyRules(cidrs []string) (string, []string, error) {
	var v4, v6 []string
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return "", nil, fmt.Errorf("sandbox: deny %q: %w", c, err)
		}
		p = p.Masked()
		if p.Addr().Is4() {
			v4 = append(v4, p.String())
		} else {
			v6 = append(v6, p.String())
		}
	}
	if len(v4)+len(v6) == 0 {
		return "", nil, nil
	}
	r := "table inet acphost { chain out { type filter hook output priority 0; policy accept;"
	if len(v4) > 0 {
		r += " ip daddr { " + strings.Join(v4, ", ") + " } reject;"
	}
	if len(v6) > 0 {
		r += " ip6 daddr { " + strings.Join(v6, ", ") + " } reject;"
	}
	return r + " }; }", append(v4, v6...), nil
}
