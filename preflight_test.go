package acphost

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/elee1766/acphost/internal/hostenv"
)

type preflightFixture struct {
	env    preflightEnv
	parent string
}

// newPreflightFixture builds a fake host where every check passes: a
// cgroup root with a delegated parent, etc/subuid and etc/subgid for the
// caller, and the helper binaries on PATH.
func newPreflightFixture(t *testing.T) preflightFixture {
	t.Helper()
	oldE, oldG := hostenv.Getegid, hostenv.Getgroups
	t.Cleanup(func() { hostenv.Getegid, hostenv.Getgroups = oldE, oldG })
	hostenv.Getegid = func() int { return 4242 }
	hostenv.Getgroups = func() ([]int, error) { return []int{4242}, nil }

	root := t.TempDir()
	cg := filepath.Join(root, "cgroup")
	parent := filepath.Join(cg, "user.slice", "sessions")
	etc := filepath.Join(root, "etc")
	bin := filepath.Join(root, "bin")
	for _, d := range []string{parent, etc, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(parent, "cgroup.controllers"), "cpu memory pids\n")
	write(t, filepath.Join(parent, "cgroup.subtree_control"), "cpu memory pids\n")
	uid := 4242
	write(t, filepath.Join(etc, "subuid"), strconv.Itoa(uid)+":100000:65536\n")
	write(t, filepath.Join(etc, "subgid"), strconv.Itoa(uid)+":100000:65536\n")
	for _, b := range []string{"newuidmap", "newgidmap", "bwrap", "pasta"} {
		if err := os.WriteFile(filepath.Join(bin, b), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return preflightFixture{
		env: preflightEnv{
			cgroupRoot: cg, etc: etc, path: bin, uid: uid,
			privileged: func(string) error { return nil },
		},
		parent: parent,
	}
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f preflightFixture) spec() Isolated {
	return Isolated{
		Cgroup:  &Cgroup{Parent: f.parent, MemoryMax: "1G", PidsMax: 100},
		User:    &User{UID: 1000, GID: 1000, MapUID: 101000, MapGID: 101000, MapCount: 1000},
		Sandbox: &Sandbox{Network: "pasta"},
	}
}

func checkFor(r PreflightReport, feature string) (PreflightCheck, bool) {
	for _, c := range r.Checks {
		if c.Feature == feature {
			return c, true
		}
	}
	return PreflightCheck{}, false
}

var allChecks = []string{
	"cgroup.parent", "cgroup.writable", "cgroup.controllers",
	"user.newuidmap", "user.newgidmap", "user.subuid", "user.subgid", "user.map",
	"sandbox.bwrap", "network.pasta",
}

func TestPreflightAllOK(t *testing.T) {
	f := newPreflightFixture(t)
	r, err := preflight(f.spec(), f.env)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Checks) != len(allChecks) || !r.OK() {
		t.Fatalf("report = %+v", r)
	}
	for i, c := range r.Checks {
		if c.Name != allChecks[i] || c.Hint != "" {
			t.Fatalf("check %d = %+v, want %s", i, c, allChecks[i])
		}
	}
	if s := r.String(); strings.Contains(s, "FAIL") || !strings.Contains(s, "ok   user.subuid") {
		t.Fatalf("text = %q", s)
	}
}

func TestPreflightNoIsolation(t *testing.T) {
	f := newPreflightFixture(t)
	r, err := preflight(Isolated{}, f.env)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Checks) != 0 {
		t.Fatalf("report = %+v", r)
	}
}

func TestPreflightNoPastaNoNetworkCheck(t *testing.T) {
	f := newPreflightFixture(t)
	r, _ := preflight(Isolated{Sandbox: &Sandbox{Network: "none"}}, f.env)
	if _, ok := checkFor(r, "network"); ok || len(r.Checks) != 1 {
		t.Fatalf("report = %+v", r)
	}
}

// TestPreflightMissing removes one piece of the fake host per case and
// expects exactly that check to fail, with its reason and fix hint.
func TestPreflightMissing(t *testing.T) {
	cases := []struct {
		name, check, reason, hint string
		mutate                    func(f *preflightFixture, s *Isolated)
	}{
		{"cgroup parent missing", "cgroup.parent", "no such file", "SetupDelegation", func(f *preflightFixture, s *Isolated) {
			s.Cgroup.Parent = filepath.Join(f.env.cgroupRoot, "nope")
		}},
		{"cgroup outside root", "cgroup.parent", "is not under", "cgroup v2 mount", func(_ *preflightFixture, s *Isolated) {
			s.Cgroup.Parent = "/tmp"
		}},
		{"cgroup relative", "cgroup.parent", "not absolute", "absolute path", func(_ *preflightFixture, s *Isolated) {
			s.Cgroup.Parent = "sessions"
		}},
		{"cgroup not writable", "cgroup.writable", "not writable", "Delegate=yes", func(f *preflightFixture, _ *Isolated) {
			if err := os.Chmod(f.parent, 0o555); err != nil {
				t.Fatal(err)
			}
		}},
		{"cgroup controller not enabled", "cgroup.controllers", "controllers memory not enabled", `echo "+memory" > `, func(f *preflightFixture, _ *Isolated) {
			write(t, filepath.Join(f.parent, "cgroup.subtree_control"), "pids\n")
		}},
		{"cgroup controller not delegated", "cgroup.controllers", "memory not in", "Delegate=", func(f *preflightFixture, _ *Isolated) {
			write(t, filepath.Join(f.parent, "cgroup.controllers"), "pids\n")
			write(t, filepath.Join(f.parent, "cgroup.subtree_control"), "pids\n")
		}},
		{"newuidmap", "user.newuidmap", "newuidmap not on PATH", "uidmap package", func(f *preflightFixture, _ *Isolated) {
			_ = os.Remove(filepath.Join(f.env.path, "newuidmap"))
		}},
		{"newgidmap", "user.newgidmap", "newgidmap not on PATH", "uidmap package", func(f *preflightFixture, _ *Isolated) {
			_ = os.Remove(filepath.Join(f.env.path, "newgidmap"))
		}},
		{"newuidmap not setuid", "user.newuidmap", "not setuid", "chmod u+s", func(f *preflightFixture, _ *Isolated) {
			f.env.privileged = func(p string) error {
				if strings.HasSuffix(p, "newuidmap") {
					return errors.New("not setuid root and has no file capabilities")
				}
				return nil
			}
		}},
		{"subuid entry missing", "user.subuid", "entries: none", "usermod --add-subuids 101000-101999 4242", func(f *preflightFixture, _ *Isolated) {
			write(t, filepath.Join(f.env.etc, "subuid"), "someone:100000:65536\n")
		}},
		{"subuid file missing", "user.subuid", "subuid range", "--add-subuids 101000-101000", func(f *preflightFixture, s *Isolated) {
			_ = os.Remove(filepath.Join(f.env.etc, "subuid"))
			s.User.MapCount = 0
		}},
		{"subgid file missing", "user.subgid", "entries: none", "usermod --add-subgids 101000-101999 4242", func(f *preflightFixture, _ *Isolated) {
			_ = os.Remove(filepath.Join(f.env.etc, "subgid"))
		}},
		{"subuid range too small", "user.subuid", "not inside one", "--add-subuids 160000-169999", func(_ *preflightFixture, s *Isolated) {
			s.User.MapUID, s.User.MapCount = 160000, 10000
		}},
		{"subgid range too small", "user.subgid", "not inside one", "--add-subgids 160000-169999", func(_ *preflightFixture, s *Isolated) {
			s.User.MapUID, s.User.MapGID, s.User.MapCount = 100000, 160000, 10000
		}},
		{"subuid malformed", "user.subuid", "line 1", "owner:start:count", func(f *preflightFixture, _ *Isolated) {
			write(t, filepath.Join(f.env.etc, "subuid"), "garbage\n")
		}},
		{"group not owned", "user.map", "group 27", "spec.user", func(_ *preflightFixture, s *Isolated) {
			s.User.Groups = []int{27}
		}},
		{"bwrap", "sandbox.bwrap", "bwrap not found", "bubblewrap", func(f *preflightFixture, _ *Isolated) {
			_ = os.Remove(filepath.Join(f.env.path, "bwrap"))
		}},
		{"pasta", "network.pasta", "pasta not found", "passt", func(f *preflightFixture, _ *Isolated) {
			_ = os.Remove(filepath.Join(f.env.path, "pasta"))
		}},
		{"nft", "network.nft", "nft not found", "nftables", func(_ *preflightFixture, s *Isolated) {
			s.Sandbox.Deny = []string{"10.0.0.0/8"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.check == "cgroup.writable" && os.Geteuid() == 0 {
				t.Skip("root can write a read-only dir")
			}
			f := newPreflightFixture(t)
			s := f.spec()
			tc.mutate(&f, &s)
			// Registered after TempDir's cleanup, so it runs first.
			t.Cleanup(func() { _ = os.Chmod(f.parent, 0o755) })
			r, err := preflight(s, f.env)
			if err != nil {
				t.Fatal(err)
			}
			if r.OK() {
				t.Fatalf("report OK, want %s missing: %+v", tc.check, r)
			}
			found := false
			for _, c := range r.Checks {
				if c.Name == tc.check {
					found = true
					if c.OK || !strings.Contains(c.Reason, tc.reason) || !strings.Contains(c.Hint, tc.hint) {
						t.Fatalf("%s = %+v, want reason %q hint %q", tc.check, c, tc.reason, tc.hint)
					}
				} else if !c.OK {
					t.Fatalf("unexpected failure %+v", c)
				}
			}
			if !found {
				t.Fatalf("no %s check: %+v", tc.check, r)
			}
			if txt := r.String(); !strings.Contains(txt, "FAIL "+tc.check+": ") || !strings.Contains(txt, "fix: ") {
				t.Fatalf("text = %q", txt)
			}
		})
	}
}

func TestPreflightNFT(t *testing.T) {
	f := newPreflightFixture(t)
	s := f.spec()
	s.Sandbox.Deny = []string{"10.0.0.0/8"}
	write(t, filepath.Join(f.env.path, "nft"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(f.env.path, "nft"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := preflight(s, f.env)
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK() || r.Checks[len(r.Checks)-1].Name != "network.nft" {
		t.Fatalf("report = %+v", r)
	}
	r, _ = preflight(f.spec(), f.env)
	for _, c := range r.Checks {
		if c.Name == "network.nft" {
			t.Fatalf("nft checked without Deny: %+v", r)
		}
	}
}

func TestSetuidOrCaps(t *testing.T) {
	p := filepath.Join(t.TempDir(), "newuidmap")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o4755); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		if err := setuidOrCaps(p); err == nil {
			t.Fatal("non-root setuid file accepted")
		}
	}
	if err := setuidOrCaps(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("missing file accepted")
	}
}
