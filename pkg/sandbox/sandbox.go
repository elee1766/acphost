package sandbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"

	"github.com/gfx-labs/acphost/pkg/trace"
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
	// Overlay lists overlay mounts: several host dirs merged onto one
	// destination, later layers winning file by file (#163).
	Overlay []Overlay `json:"overlay,omitempty"`
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
	// DNS forwards DNS queries from an address inside pasta's namespace
	// to a host resolver. Only valid with "pasta". Nil leaves pasta's
	// default. See docs/notes/124-sandbox-dns.md.
	DNS *DNS `json:"dns,omitempty"`
	// ResolvConf is a host file bound read-only over /etc/resolv.conf in
	// the sandbox, typically naming DNS.Forward. Empty binds nothing.
	ResolvConf string `json:"resolv_conf,omitempty"`
	// ExtraArgs are passed to bwrap after everything else, before "--".
	ExtraArgs []string `json:"extra_args,omitempty"`
	// OpenParents makes bwrap create the parents of mount points 0755
	// instead of 0700 owned by the sandbox root, so an agent that drops
	// from root to another uid can reach its mounts (#162). Set by the
	// host, not part of the spec.
	OpenParents bool `json:"-"`
}

// DNS is pasta's DNS forwarding. Queries the agent sends to Forward (an
// address inside the namespace) go to Host on the host side, which may be
// a loopback resolver such as systemd-resolved's 127.0.0.53 that the
// namespace cannot reach directly.
type DNS struct {
	// Forward is passed as --dns-forward. Required.
	Forward string `json:"forward"`
	// Host is passed as --dns-host. Empty keeps pasta's default (the
	// host's first resolv.conf nameserver).
	Host string `json:"host,omitempty"`
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

// Overlay is one overlay mount. bwrap reads each layer from the host, so
// a layer cannot be another overlay inside the sandbox: pass one flat
// list per destination.
type Overlay struct {
	Dst string `json:"dst"`
	// Layers are host dirs, lowest first. At least one, at most
	// MaxOverlayLayers, all existing.
	Layers []string `json:"layers"`
	// Mode is "ro" (default, read-only), "tmp" (writes go to a tmpfs and
	// are lost) or "persist" (writes go to Upper).
	Mode string `json:"mode,omitempty"`
	// Upper and Work are host dirs for Mode "persist": on the same
	// filesystem, not inside a layer, Work empty.
	Upper string `json:"upper,omitempty"`
	Work  string `json:"work,omitempty"`
}

// MaxOverlayLayers bounds Overlay.Layers.
const MaxOverlayLayers = 500

// args checks o and returns its bwrap flags.
func (o Overlay) args() ([]string, error) {
	fail := func(format string, a ...any) ([]string, error) {
		return nil, fmt.Errorf("sandbox: overlay %s: "+format, append([]any{o.Dst}, a...)...)
	}
	if !filepath.IsAbs(o.Dst) {
		return fail("dst must be absolute")
	}
	if len(o.Layers) == 0 {
		return fail("no layers")
	}
	if len(o.Layers) > MaxOverlayLayers {
		return fail("%d layers, over the limit of %d", len(o.Layers), MaxOverlayLayers)
	}
	var out []string
	for _, l := range o.Layers {
		if !filepath.IsAbs(l) {
			return fail("layer %s is not absolute", l)
		}
		st, err := os.Stat(l)
		if err != nil {
			return fail("layer: %w", err)
		}
		if !st.IsDir() {
			return fail("layer %s is not a directory", l)
		}
		out = append(out, "--overlay-src", filepath.Clean(l))
	}
	dst := filepath.Clean(o.Dst)
	switch o.Mode {
	case "", "ro":
		if o.Upper != "" || o.Work != "" {
			return fail("upper and work need mode persist")
		}
		return append(out, "--ro-overlay", dst), nil
	case "tmp":
		if o.Upper != "" || o.Work != "" {
			return fail("upper and work need mode persist")
		}
		return append(out, "--tmp-overlay", dst), nil
	case "persist":
	default:
		return fail("unknown mode %q", o.Mode)
	}
	if !filepath.IsAbs(o.Upper) || !filepath.IsAbs(o.Work) {
		return fail("persist needs absolute upper and work")
	}
	upper, work := filepath.Clean(o.Upper), filepath.Clean(o.Work)
	if within(upper, work) || within(work, upper) {
		return fail("upper and work must not nest")
	}
	for _, l := range o.Layers {
		l = filepath.Clean(l)
		if within(upper, l) || within(work, l) {
			return fail("upper and work must not be inside layer %s", l)
		}
	}
	var us, ws syscall.Stat_t
	if err := syscall.Stat(upper, &us); err != nil {
		return fail("upper: %w", err)
	}
	if err := syscall.Stat(work, &ws); err != nil {
		return fail("work: %w", err)
	}
	if us.Dev != ws.Dev {
		return fail("upper and work are on different filesystems")
	}
	ents, err := os.ReadDir(work)
	if err != nil {
		return fail("work: %w", err)
	}
	if len(ents) > 0 {
		return fail("work %s is not empty", work)
	}
	return append(out, "--overlay", upper, work, dst), nil
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

// openParents returns --dir options, mode 0755, for the parents of dst
// not yet in done, shallowest first, and records them in done. bwrap
// would make them 0700. A parent inside a bind mount is a host dir and
// is skipped (and a mount point already exists), so only dirs in the
// sandbox's own tmpfs are made.
func openParents(dst string, binds []string, done map[string]bool) []string {
	var ps []string
	for p := filepath.Dir(dst); !done[p]; p = filepath.Dir(p) {
		done[p] = true
		if !slices.ContainsFunc(binds, func(m string) bool { return within(p, m) }) {
			ps = append(ps, p)
		}
	}
	var out []string
	for i := len(ps) - 1; i >= 0; i-- {
		out = append(out, "--perms", "0755", "--dir", ps[i])
	}
	return out
}

func within(p, root string) bool {
	if root == "/" {
		return strings.HasPrefix(p, "/")
	}
	return p == root || strings.HasPrefix(p, root+"/")
}

// Args returns the full bwrap argv, starting with the bwrap path,
// that runs cmd with args in dir. It reads only the filesystem (to check
// sources) and the environment (for EnvKeep and PATH); the same inputs
// give the same output. dir must lie inside an RW mount's destination.
func Args(s Sandbox, cmd string, args []string, dir string) ([]string, error) {
	argv, _, err := BuildArgs(s, cmd, args, dir)
	return argv, err
}

// BuildArgs is Args that also reports the network setup the
// argv applies, for isolation.json.
func BuildArgs(s Sandbox, cmd string, args []string, dir string) ([]string, *NetworkApplied, error) {
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
	if s.DNS != nil && !pasta {
		return fail(fmt.Errorf("sandbox: dns needs network pasta, not %q", s.Network))
	}
	if s.ResolvConf != "" {
		if _, err := os.Stat(s.ResolvConf); err != nil {
			return fail(fmt.Errorf("sandbox: resolv_conf: %w", err))
		}
	}
	net := &NetworkApplied{Mode: s.Network, ResolvConf: s.ResolvConf}
	if net.Mode == "" {
		net.Mode = "host"
	}
	var prefix []string
	if pasta {
		var err error
		if prefix, net.Deny, err = PastaPrefix(s); err != nil {
			return fail(err)
		}
		if s.DNS != nil {
			net.DNSForward, net.DNSHost = s.DNS.Forward, s.DNS.Host
		}
	}
	bwrap := s.Bwrap
	if bwrap == "" {
		p, err := trace.LookPath("bwrap")
		if err != nil {
			return fail(fmt.Errorf("sandbox: %w", err))
		}
		bwrap = p
	}

	type bind struct {
		flag, src, dst string
		overlay        []string // full flags of an overlay mount
	}
	var binds []bind
	for _, m := range s.RO {
		if _, err := os.Stat(m.Src); err != nil {
			if errors.Is(err, os.ErrNotExist) && m.SkipMissing {
				continue
			}
			return fail(fmt.Errorf("sandbox: ro %s: %w", m.Src, err))
		}
		binds = append(binds, bind{flag: "--ro-bind", src: m.Src, dst: m.dst()})
	}
	for _, m := range s.RW {
		if _, err := os.Stat(m.Src); err != nil {
			return fail(fmt.Errorf("sandbox: rw %s: %w", m.Src, err))
		}
		binds = append(binds, bind{flag: "--bind", src: m.Src, dst: m.dst()})
	}
	for _, m := range s.Tmpfs {
		binds = append(binds, bind{flag: "--tmpfs", dst: m.dst()})
	}
	for _, o := range s.Overlay {
		a, err := o.args()
		if err != nil {
			return fail(err)
		}
		binds = append(binds, bind{dst: filepath.Clean(o.Dst), overlay: a})
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
	// With OpenParents, each mount is preceded by --dir for its missing
	// parents. Binds are parents first, so a parent is never made
	// before a mount it sits in. Binds and overlays show host content,
	// so a dir inside one is never made.
	var bindDsts []string
	for _, b := range binds {
		if b.flag != "--tmpfs" {
			bindDsts = append(bindDsts, b.dst)
		}
	}
	// bwrap's own mounts exist already; --perms would chmod them.
	done := map[string]bool{"/": true, "/proc": true, "/dev": true, "/tmp": true}
	parents := func(dst string) {
		if s.OpenParents {
			argv = append(argv, openParents(dst, bindDsts, done)...)
		}
	}
	for _, b := range binds {
		parents(b.dst)
		switch {
		case b.overlay != nil:
			argv = append(argv, b.overlay...)
		case b.flag == "--tmpfs":
			argv = append(argv, b.flag, b.dst)
		default:
			argv = append(argv, b.flag, b.src, b.dst)
		}
	}
	for _, h := range s.Hide {
		parents(filepath.Clean(h))
		argv = append(argv, "--tmpfs", filepath.Clean(h))
	}
	// After all binds, so a bind of /etc does not cover it.
	if s.ResolvConf != "" {
		dst := resolvConfDst(etcResolvConf)
		parents(dst)
		argv = append(argv, "--ro-bind", s.ResolvConf, dst)
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

// etcResolvConf is the path ResolvConf replaces. A variable for tests.
var etcResolvConf = "/etc/resolv.conf"

// resolvConfDst returns where to bind ResolvConf so that path reads it
// inside the sandbox. On systemd-resolved hosts path is a symlink into
// /run, which the sandbox usually lacks: bwrap cannot create a file
// through a dangling link, so bind at the link's final target instead
// (bwrap makes the missing parents on its own root). The sandbox's /etc
// is assumed to come from the host's, as with an /etc bind.
func resolvConfDst(path string) string {
	for range 8 {
		t, err := os.Readlink(path)
		if err != nil {
			return path
		}
		if !filepath.IsAbs(t) {
			t = filepath.Join(filepath.Dir(path), t)
		}
		path = filepath.Clean(t)
	}
	return path
}

// NFTBinary is the command spawn and Preflight both resolve on PATH for Deny.
const NFTBinary = "nft"

// PastaPrefix returns the argv that runs bwrap under pasta. pasta creates
// the network namespace, configures it, and only then starts its command,
// so the agent never sees a namespace without its interface or rules.
// With Deny, a shell loads nftables rules before exec'ing bwrap; exit 125
// means the rules failed to load.
// It also returns the normalized deny CIDRs the rules reject.
func PastaPrefix(s Sandbox) ([]string, []string, error) {
	deny, cidrs, err := denyRules(s.Deny)
	if err != nil {
		return nil, nil, err
	}
	p := s.Pasta
	if p == "" {
		if p, err = trace.LookPath("pasta"); err != nil {
			return nil, nil, fmt.Errorf("sandbox: %w", err)
		}
	}
	// Foreground, quiet, and no inbound port forwards: the agent only
	// makes outbound connections.
	argv := []string{p, "--config-net", "--foreground", "--quiet", "-t", "none", "-u", "none", "-T", "none", "-U", "none"}
	if s.DNS != nil {
		dns, err := dnsArgs(*s.DNS, cidrs)
		if err != nil {
			return nil, nil, err
		}
		argv = append(argv, dns...)
	}
	argv = append(argv, "--")
	if deny == "" {
		return argv, nil, nil
	}
	sh, err := trace.LookPath("sh")
	if err != nil {
		return nil, nil, fmt.Errorf("sandbox: %w", err)
	}
	nft, err := trace.LookPath(NFTBinary)
	if err != nil {
		return nil, nil, fmt.Errorf("sandbox: deny: %w", err)
	}
	return append(argv, sh, "-c", `"$1" "$2" || exit 125; shift 2; exec "$@"`, "acphost-net", nft, deny), cidrs, nil
}

// dnsArgs validates d and returns pasta's DNS options. Forward must not
// fall in a deny CIDR: the rules apply to it like any other destination,
// so every lookup would be rejected.
func dnsArgs(d DNS, deny []string) ([]string, error) {
	fwd, err := netip.ParseAddr(d.Forward)
	if err != nil {
		return nil, fmt.Errorf("sandbox: dns forward %q: %w", d.Forward, err)
	}
	for _, c := range deny {
		if netip.MustParsePrefix(c).Contains(fwd) {
			return nil, fmt.Errorf("sandbox: dns forward %s is inside deny %s", fwd, c)
		}
	}
	args := []string{"--dns-forward", fwd.String()}
	if d.Host != "" {
		h, err := netip.ParseAddr(d.Host)
		if err != nil {
			return nil, fmt.Errorf("sandbox: dns host %q: %w", d.Host, err)
		}
		args = append(args, "--dns-host", h.String())
	}
	return args, nil
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

// NetworkApplied is the sandbox network mode ("host", "none" or
// "pasta"), the normalized deny CIDRs loaded as rules, pasta's DNS
// forward, the file bound over /etc/resolv.conf, and whether the agent
// started under pasta.
type NetworkApplied struct {
	Mode         string   `json:"mode"`
	Deny         []string `json:"deny,omitempty"`
	DNSForward   string   `json:"dns_forward,omitempty"`
	DNSHost      string   `json:"dns_host,omitempty"`
	ResolvConf   string   `json:"resolv_conf,omitempty"`
	PastaStarted bool     `json:"pasta_started,omitempty"`
}
