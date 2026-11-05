package acphost

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// PreflightCheck is the state of one isolation prerequisite.
type PreflightCheck struct {
	// Feature is "cgroup", "user_namespace", "sandbox" or "network".
	Feature string `json:"feature"`
	// OK reports whether the prerequisite is present.
	OK bool `json:"ok"`
	// Reason says what is missing when OK is false.
	Reason string `json:"reason,omitempty"`
}

// PreflightReport lists a check for each isolation feature the spec asks
// for. Features the spec does not ask for are absent.
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

// preflightEnv holds the roots Preflight reads, injectable for tests.
type preflightEnv struct {
	cgroupRoot string // cgroupfs mount
	etc        string // directory holding subuid and subgid
	path       string // PATH used to find helper binaries
	uid        int
}

// Preflight checks the host prerequisites for the isolation s asks for
// (cgroup, user namespace, sandbox, pasta network) without spawning
// anything or writing anything, so a caller can quarantine a bad host
// config once at load time instead of failing every spawn (#58).
func Preflight(s Spec) (PreflightReport, error) {
	return preflight(s, preflightEnv{
		cgroupRoot: "/sys/fs/cgroup",
		etc:        filepath.Dir(subuidPath),
		path:       os.Getenv("PATH"),
		uid:        os.Geteuid(),
	})
}

func preflight(s Spec, env preflightEnv) (PreflightReport, error) {
	var r PreflightReport
	add := func(feature string, err error) {
		c := PreflightCheck{Feature: feature, OK: err == nil}
		if err != nil {
			c.Reason = err.Error()
		}
		r.Checks = append(r.Checks, c)
	}
	if s.Cgroup != nil {
		add("cgroup", preflightCgroup(*s.Cgroup, env))
	}
	if s.User != nil {
		add("user_namespace", preflightUser(*s.User, env))
	}
	if s.Sandbox != nil {
		add("sandbox", preflightBinary(s.Sandbox.Bwrap, "bwrap", env))
		if s.Sandbox.Network == "pasta" {
			add("network", preflightBinary(s.Sandbox.Pasta, "pasta", env))
		}
	}
	return r, nil
}

func preflightCgroup(c Cgroup, env preflightEnv) error {
	root := filepath.Clean(env.cgroupRoot)
	if !filepath.IsAbs(c.Parent) {
		return fmt.Errorf("parent %q is not absolute", c.Parent)
	}
	parent := filepath.Clean(c.Parent)
	if parent != root && !strings.HasPrefix(parent, root+"/") {
		return fmt.Errorf("parent %s is not under %s", parent, root)
	}
	st, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("parent: %w", err)
	}
	if !st.IsDir() {
		return fmt.Errorf("parent %s is not a directory", parent)
	}
	if syscall.Access(parent, 2 /* W_OK */) != nil {
		return fmt.Errorf("parent %s is not writable (not delegated)", parent)
	}
	l := c.limits()
	if len(l) == 0 {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(parent, "cgroup.subtree_control"))
	if err != nil {
		return fmt.Errorf("parent: %w", err)
	}
	enabled := strings.Fields(string(b))
	var missing []string
	for _, x := range l {
		if !slices.Contains(enabled, x.controller) && !slices.Contains(missing, x.controller) {
			missing = append(missing, x.controller)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("controllers %s not enabled in %s/cgroup.subtree_control", strings.Join(missing, ","), parent)
	}
	return nil
}

func preflightUser(u User, env preflightEnv) error {
	for _, c := range []string{"newuidmap", "newgidmap"} {
		if err := lookCommand(c, []string{"PATH=" + env.path}); err != nil {
			return fmt.Errorf("%s not on PATH", c)
		}
	}
	subuidFile := filepath.Join(env.etc, "subuid")
	subuid, err := readSubIDs(subuidFile, env.uid)
	if err != nil {
		return fmt.Errorf("%s: %w", subuidFile, err)
	}
	subgidFile := filepath.Join(env.etc, "subgid")
	subgid, err := readSubIDs(subgidFile, env.uid)
	if err != nil {
		return fmt.Errorf("%s: %w", subgidFile, err)
	}
	_, _, err = userMaps(u, env.uid, subuid, subgid)
	return err
}

// preflightBinary checks explicit (a configured path or name) or, when
// empty, name on PATH.
func preflightBinary(explicit, name string, env preflightEnv) error {
	cmd := name
	if explicit != "" {
		cmd = explicit
	}
	if err := lookCommand(cmd, []string{"PATH=" + env.path}); err != nil {
		return fmt.Errorf("%s not found: %w", cmd, err)
	}
	return nil
}
