package acphost

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type preflightFixture struct {
	env    preflightEnv
	parent string
}

func newPreflightFixture(t *testing.T) preflightFixture {
	t.Helper()
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
		env:    preflightEnv{cgroupRoot: cg, etc: etc, path: bin, uid: uid},
		parent: parent,
	}
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f preflightFixture) spec() Spec {
	return Spec{
		Cgroup:  &Cgroup{Parent: f.parent, MemoryMax: "1G", PidsMax: 100},
		User:    &User{UID: 1000, GID: 1000, MapUID: 101000, MapGID: 101000},
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

func TestPreflightAllOK(t *testing.T) {
	f := newPreflightFixture(t)
	r, err := preflight(f.spec(), f.env)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Checks) != 4 || !r.OK() {
		t.Fatalf("report = %+v", r)
	}
}

func TestPreflightNoIsolation(t *testing.T) {
	f := newPreflightFixture(t)
	r, err := preflight(Spec{Command: "agent"}, f.env)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Checks) != 0 {
		t.Fatalf("report = %+v", r)
	}
}

func TestPreflightNoPastaNoNetworkCheck(t *testing.T) {
	f := newPreflightFixture(t)
	r, _ := preflight(Spec{Sandbox: &Sandbox{Network: "none"}}, f.env)
	if _, ok := checkFor(r, "network"); ok || len(r.Checks) != 1 {
		t.Fatalf("report = %+v", r)
	}
}

func TestPreflightMissing(t *testing.T) {
	cases := []struct {
		name, feature, reason string
		mutate                func(f *preflightFixture, s *Spec)
	}{
		{"cgroup parent missing", "cgroup", "no such file", func(f *preflightFixture, s *Spec) {
			s.Cgroup.Parent = filepath.Join(f.env.cgroupRoot, "nope")
		}},
		{"cgroup outside root", "cgroup", "is not under", func(_ *preflightFixture, s *Spec) {
			s.Cgroup.Parent = "/tmp"
		}},
		{"cgroup controller", "cgroup", "controllers memory", func(f *preflightFixture, _ *Spec) {
			write(t, filepath.Join(f.parent, "cgroup.subtree_control"), "pids\n")
		}},
		{"newuidmap", "user_namespace", "newuidmap not on PATH", func(f *preflightFixture, _ *Spec) {
			_ = os.Remove(filepath.Join(f.env.path, "newuidmap"))
		}},
		{"newgidmap", "user_namespace", "newgidmap not on PATH", func(f *preflightFixture, _ *Spec) {
			_ = os.Remove(filepath.Join(f.env.path, "newgidmap"))
		}},
		{"subuid range", "user_namespace", "subuid range", func(_ *preflightFixture, s *Spec) {
			s.User.MapUID = 500000
		}},
		{"subgid range", "user_namespace", "subgid range", func(f *preflightFixture, s *Spec) {
			_ = os.Remove(filepath.Join(f.env.etc, "subgid"))
			s.User.MapGID = 987654
		}},
		{"subuid span", "user_namespace", "not inside one", func(_ *preflightFixture, s *Spec) {
			s.User.MapUID, s.User.MapGID, s.User.MapCount = 160000, 100000, 10000
		}},
		{"subuid malformed", "user_namespace", "line 1", func(f *preflightFixture, _ *Spec) {
			write(t, filepath.Join(f.env.etc, "subuid"), "garbage\n")
		}},
		{"bwrap", "sandbox", "bwrap not found", func(f *preflightFixture, _ *Spec) {
			_ = os.Remove(filepath.Join(f.env.path, "bwrap"))
		}},
		{"pasta", "network", "pasta not found", func(f *preflightFixture, _ *Spec) {
			_ = os.Remove(filepath.Join(f.env.path, "pasta"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPreflightFixture(t)
			s := f.spec()
			tc.mutate(&f, &s)
			r, err := preflight(s, f.env)
			if err != nil {
				t.Fatal(err)
			}
			if r.OK() {
				t.Fatalf("report OK, want %s missing: %+v", tc.feature, r)
			}
			for _, c := range r.Checks {
				if c.Feature == tc.feature {
					if c.OK || !strings.Contains(c.Reason, tc.reason) {
						t.Fatalf("%s = %+v, want reason containing %q", tc.feature, c, tc.reason)
					}
				} else if !c.OK {
					t.Fatalf("unexpected failure %+v", c)
				}
			}
		})
	}
}
