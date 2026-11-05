package acphost

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// PreflightCheck is the state of one isolation prerequisite.
type PreflightCheck struct {
	// Feature is "cgroup", "user_namespace", "sandbox" or "network".
	Feature string `json:"feature"`
	// Name says which prerequisite of Feature this is, such as
	// "cgroup.controllers" or "user.subuid". See docs/notes/115-preflight-hints.md.
	Name string `json:"name"`
	// OK reports whether the prerequisite is present.
	OK bool `json:"ok"`
	// Reason says what is missing when OK is false.
	Reason string `json:"reason,omitempty"`
	// Hint is a host config change that would fix it, when OK is false.
	Hint string `json:"hint,omitempty"`
}

// String formats a check as "ok   name", or for a failure
// "FAIL name: reason" and an indented "fix: hint" line.
func (c PreflightCheck) String() string {
	if c.OK {
		return "ok   " + c.Name
	}
	s := "FAIL " + c.Name + ": " + c.Reason
	if c.Hint != "" {
		s += "\n     fix: " + c.Hint
	}
	return s
}

// PreflightReport lists the checks for each isolation feature the spec
// asks for. Features the spec does not ask for are absent.
type PreflightReport struct {
	Checks []PreflightCheck `json:"checks"`
}

// OK reports whether every check passed.
func (r PreflightReport) OK() bool {
	for _, c := range r.Checks {
		if !c.OK {
			return false
		}
	}
	return true
}

// String is the report text: one line per check, with a fix line under
// each failure.
func (r PreflightReport) String() string {
	lines := make([]string, len(r.Checks))
	for i, c := range r.Checks {
		lines[i] = c.String()
	}
	return strings.Join(lines, "\n")
}

// preflightEnv holds the roots Preflight reads, injectable for tests.
type preflightEnv struct {
	cgroupRoot string // cgroupfs mount
	etc        string // directory holding subuid and subgid
	path       string // PATH used to find helper binaries
	uid        int
	// privileged reports why the binary at path cannot gain the
	// privileges newuidmap needs, or nil. Tests replace it since they
	// cannot make root-owned setuid files.
	privileged func(path string) error
}

// Preflight checks the host prerequisites for the isolation s asks for
// (cgroup, user namespace, sandbox, pasta network) without spawning
// anything or writing anything, so a caller can quarantine a bad host
// config once at load time instead of failing every spawn (#58). Each
// failed check carries a Hint with the host config change that fixes it
// (#115).
func Preflight(s Spec) (PreflightReport, error) {
	return preflight(s, preflightEnv{
		cgroupRoot: "/sys/fs/cgroup",
		etc:        filepath.Dir(subuidPath),
		path:       os.Getenv("PATH"),
		uid:        os.Geteuid(),
		privileged: setuidOrCaps,
	})
}

// failure is a failed check: what is wrong and how to fix it.
type failure struct{ reason, hint string }

func fail(hint, format string, a ...any) *failure {
	return &failure{reason: fmt.Sprintf(format, a...), hint: hint}
}

func preflight(s Spec, env preflightEnv) (PreflightReport, error) {
	var r PreflightReport
	add := func(feature, name string, f *failure) {
		c := PreflightCheck{Feature: feature, Name: name, OK: f == nil}
		if f != nil {
			c.Reason, c.Hint = f.reason, f.hint
		}
		r.Checks = append(r.Checks, c)
	}
	if s.Cgroup != nil {
		parent, f := preflightCgroupParent(*s.Cgroup, env)
		add("cgroup", "cgroup.parent", f)
		if f == nil {
			add("cgroup", "cgroup.writable", preflightCgroupWritable(parent, env))
			add("cgroup", "cgroup.controllers", preflightCgroupControllers(*s.Cgroup, parent))
		}
	}
	if s.User != nil {
		for _, c := range []string{"newuidmap", "newgidmap"} {
			add("user_namespace", "user."+c, preflightIDMapper(c, env))
		}
		subuid, fu := preflightSubIDs(*s.User, "uid", env)
		add("user_namespace", "user.subuid", fu)
		subgid, fg := preflightSubIDs(*s.User, "gid", env)
		add("user_namespace", "user.subgid", fg)
		if fu == nil && fg == nil {
			add("user_namespace", "user.map", preflightUserMap(*s.User, env, subuid, subgid))
		}
	}
	if s.Sandbox != nil {
		add("sandbox", "sandbox.bwrap", preflightBinary(s.Sandbox.Bwrap, "bwrap",
			"install bubblewrap (apt install bubblewrap, dnf install bubblewrap) or set sandbox.bwrap to its path", env))
		if s.Sandbox.Network == "pasta" {
			add("network", "network.pasta", preflightBinary(s.Sandbox.Pasta, "pasta",
				"install passt, which ships pasta (apt install passt, dnf install passt), or set sandbox.pasta to its path", env))
		}
		// Spawn looks nft up on PATH only when Deny is set (pastaPrefix).
		if len(s.Sandbox.Deny) > 0 {
			add("network", "network.nft", preflightBinary("", nftBinary,
				"install nftables (apt install nftables, dnf install nftables) so nft is on PATH", env))
		}
	}
	return r, nil
}

const hintSetupDelegation = "create it inside the host's delegated cgroup: acphost.SetupDelegation(acphost.DelegatedCgroup().Path, controllers) returns <own>/sessions for cgroup.parent"

func preflightCgroupParent(c Cgroup, env preflightEnv) (string, *failure) {
	root := filepath.Clean(env.cgroupRoot)
	if !filepath.IsAbs(c.Parent) {
		return "", fail("set cgroup.parent to an absolute path under "+root, "parent %q is not absolute", c.Parent)
	}
	parent := filepath.Clean(c.Parent)
	if parent != root && !strings.HasPrefix(parent, root+"/") {
		return "", fail("set cgroup.parent to a directory under "+root+" (cgroup v2 mount)", "parent %s is not under %s", parent, root)
	}
	st, err := os.Stat(parent)
	if err != nil {
		return "", fail(hintSetupDelegation, "parent: %v", err)
	}
	if !st.IsDir() {
		return "", fail(hintSetupDelegation, "parent %s is not a directory", parent)
	}
	return parent, nil
}

func preflightCgroupWritable(parent string, env preflightEnv) *failure {
	if syscall.Access(parent, 2 /* W_OK */) == nil {
		return nil
	}
	return fail(fmt.Sprintf("run the host under a systemd unit with Delegate=yes (or systemd-run --user --scope -p Delegate=yes), or as root: chown -R %d %s", env.uid, parent),
		"parent %s is not writable (not delegated)", parent)
}

func preflightCgroupControllers(c Cgroup, parent string) *failure {
	var need []string
	for _, x := range c.limits() {
		if !slices.Contains(need, x.controller) {
			need = append(need, x.controller)
		}
	}
	if len(need) == 0 {
		return nil
	}
	ctl := filepath.Join(parent, "cgroup.subtree_control")
	b, err := os.ReadFile(ctl)
	if err != nil {
		return fail(hintSetupDelegation, "parent: %v", err)
	}
	enabled := strings.Fields(string(b))
	avail := readFields(filepath.Join(parent, "cgroup.controllers"))
	var missing, undelegated []string
	for _, x := range need {
		if slices.Contains(enabled, x) {
			continue
		}
		missing = append(missing, x)
		if !slices.Contains(avail, x) {
			undelegated = append(undelegated, x)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	reason := fmt.Sprintf("controllers %s not enabled in %s", strings.Join(missing, ","), ctl)
	if len(undelegated) > 0 {
		return fail(fmt.Sprintf("controllers %s are not delegated to this cgroup: add them to the unit's Delegate= line (for user@.service: /etc/systemd/system/user@.service.d/delegate.conf with [Service] and Delegate=cpu cpuset io memory pids), restart it, then enable them in %s", strings.Join(undelegated, " "), ctl),
			"%s (%s not in %s/cgroup.controllers)", reason, strings.Join(undelegated, ","), parent)
	}
	plus := make([]string, len(missing))
	for i, x := range missing {
		plus[i] = "+" + x
	}
	return fail(fmt.Sprintf("echo %q > %s, or use acphost.SetupDelegation", strings.Join(plus, " "), ctl), "%s", reason)
}

// preflightIDMapper checks newuidmap or newgidmap is on PATH and can gain
// the privileges it needs (setuid root or file capabilities).
func preflightIDMapper(name string, env preflightEnv) *failure {
	const install = "install the uidmap package (apt install uidmap; shadow-utils on Fedora)"
	p, err := resolveCommand(name, env.path)
	if err != nil {
		return fail(install, "%s not on PATH", name)
	}
	if env.privileged != nil {
		if err := env.privileged(p); err != nil {
			return fail(fmt.Sprintf("reinstall the uidmap package, or as root: chown root %s && chmod u+s %s", p, p), "%s: %v", p, err)
		}
	}
	return nil
}

// setuidOrCaps accepts a root-owned setuid binary or one with file
// capabilities (Fedora ships newuidmap with cap_setuid instead of setuid).
func setuidOrCaps(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && sys.Uid == 0 && st.Mode()&os.ModeSetuid != 0 {
		return nil
	}
	if n, err := syscall.Getxattr(path, "security.capability", nil); err == nil && n > 0 {
		return nil
	}
	return errors.New("not setuid root and has no file capabilities")
}

// resolveCommand finds cmd (a path or a name on path) as an executable
// file.
func resolveCommand(cmd, path string) (string, error) {
	if strings.Contains(cmd, "/") {
		return cmd, lookCommand(cmd, nil)
	}
	for _, d := range filepath.SplitList(path) {
		if d == "" {
			d = "."
		}
		p := filepath.Join(d, cmd)
		if lookCommand(p, nil) == nil {
			return p, nil
		}
	}
	return "", errors.New("not found")
}

// preflightSubIDs checks the caller's subuid (kind "uid") or subgid entry
// covers the outside ids u maps.
func preflightSubIDs(u User, kind string, env preflightEnv) ([]Range, *failure) {
	file := filepath.Join(env.etc, "sub"+kind)
	start, flag := u.MapUID, "--add-subuids"
	if kind == "gid" {
		start, flag = u.MapGID, "--add-subgids"
	}
	who := strconv.Itoa(env.uid)
	if pw, err := user.LookupId(who); err == nil {
		who = pw.Username
	}
	rs, err := readSubIDs(file, env.uid)
	if err != nil {
		return nil, fail("fix or remove the bad line in "+file+" (format owner:start:count)", "%s: %v", file, err)
	}
	n := max(u.MapCount, 1)
	hint := fmt.Sprintf("as root: usermod %s %d-%d %s", flag, start, start+n-1, who)
	if u.MapCount > 1 {
		if err := checkSpan(kind, file, start, n, rs); err != nil {
			return nil, &failure{reason: err.Error(), hint: hint}
		}
	}
	switch {
	case kind == "uid" && u.MapUID != env.uid && !inRanges(u.MapUID, rs):
		return nil, fail(hint, "user: map_uid %d is not uid %d or in its subuid range (%s)", u.MapUID, env.uid, file)
	case kind == "gid" && !callerHoldsGID(u.MapGID) && !inRanges(u.MapGID, rs):
		return nil, fail(hint, "user: map_gid %d is not held by the caller or in its subgid range (%s)", u.MapGID, file)
	}
	return rs, nil
}

func callerHoldsGID(g int) bool {
	if g == getegid() {
		return true
	}
	gs, err := getgroups()
	return err == nil && slices.Contains(gs, g)
}

// preflightUserMap runs the rest of the spawn checks (groups, duplicate
// gids) once the subid entries are known good.
func preflightUserMap(u User, env preflightEnv, subuid, subgid []Range) *failure {
	_, _, err := userMaps(u, env.uid, filepath.Join(env.etc, "subuid"), filepath.Join(env.etc, "subgid"), subuid, subgid)
	if err != nil {
		return fail("fix spec.user: each group must be held by the caller or in its subgid range, and no outside gid may be mapped twice", "%v", err)
	}
	return nil
}

// preflightBinary checks explicit (a configured path or name) or, when
// empty, name on PATH.
func preflightBinary(explicit, name, hint string, env preflightEnv) *failure {
	cmd := name
	if explicit != "" {
		cmd = explicit
	}
	if err := lookCommand(cmd, []string{"PATH=" + env.path}); err != nil {
		return fail(hint, "%s not found: %v", cmd, err)
	}
	return nil
}
