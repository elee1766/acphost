package sandbox

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// OpenParents makes the tmpfs parents of each mount 0755, before the
// mount, and leaves dirs inside a bind (host dirs) alone (#162).
func TestSandboxOpenParents(t *testing.T) {
	tmp := t.TempDir()
	s := Sandbox{
		Bwrap: "/bin/bwrap", OpenParents: true,
		RO:   []Mount{{Src: tmp, Dst: "/a/b"}, {Src: tmp, Dst: "/a/b/c/d"}},
		RW:   []Mount{{Src: tmp, Dst: "/w/x"}},
		Hide: []string{"/a/e/f"},
	}
	got, err := Args(s, "agent", nil, "/w/x")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/bin/bwrap", "--die-with-parent", "--unshare-pid", "--new-session",
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp",
		"--perms", "0755", "--dir", "/a", "--ro-bind", tmp, "/a/b",
		"--perms", "0755", "--dir", "/w", "--bind", tmp, "/w/x",
		"--ro-bind", tmp, "/a/b/c/d",
		"--perms", "0755", "--dir", "/a/e", "--tmpfs", "/a/e/f",
		"--clearenv", "--chdir", "/w/x", "--", "agent",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
}

func TestSandboxArgs(t *testing.T) {
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	missing := filepath.Join(tmp, "missing")
	t.Setenv("ACPHOST_KEEP", "v1")
	defaults := []string{
		"/bin/bwrap", "--die-with-parent", "--unshare-pid", "--new-session",
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp",
	}
	cat := slices.Concat[[]string]

	tests := []struct {
		name string
		s    Sandbox
		dir  string
		want []string
		err  string
	}{
		{
			name: "defaults",
			s:    Sandbox{Bwrap: "/bin/bwrap", RW: []Mount{{Src: tmp}}},
			dir:  tmp,
			want: cat(defaults, []string{"--bind", tmp, tmp, "--clearenv", "--chdir", tmp, "--", "agent", "-x"}),
		},
		{
			name: "order",
			s: Sandbox{
				Bwrap:     "/bin/bwrap",
				RW:        []Mount{{Src: tmp, Dst: "/a/b"}},
				RO:        []Mount{{Src: tmp, Dst: "/a"}, {Src: tmp, Dst: "/a/b"}},
				Tmpfs:     []Mount{{Src: "/a/b/c"}},
				Hide:      []string{"/a/secret"},
				ExtraArgs: []string{"--x"},
			},
			dir: "/a/b",
			want: cat(defaults, []string{
				"--ro-bind", tmp, "/a",
				"--ro-bind", tmp, "/a/b",
				"--bind", tmp, "/a/b",
				"--tmpfs", "/a/b/c",
				"--tmpfs", "/a/secret",
				"--clearenv", "--chdir", "/a/b", "--x", "--", "agent", "-x",
			}),
		},
		{
			name: "missing rw",
			s:    Sandbox{Bwrap: "/bin/bwrap", RW: []Mount{{Src: missing}}},
			dir:  missing,
			err:  "rw",
		},
		{
			name: "missing ro",
			s:    Sandbox{Bwrap: "/bin/bwrap", RW: []Mount{{Src: tmp}}, RO: []Mount{{Src: missing}}},
			dir:  tmp,
			err:  "ro",
		},
		{
			name: "missing ro skip",
			s:    Sandbox{Bwrap: "/bin/bwrap", RW: []Mount{{Src: tmp}}, RO: []Mount{{Src: missing, SkipMissing: true}}},
			dir:  tmp,
			want: cat(defaults, []string{"--bind", tmp, tmp, "--clearenv", "--chdir", tmp, "--", "agent", "-x"}),
		},
		{
			name: "network none",
			s:    Sandbox{Bwrap: "/bin/bwrap", RW: []Mount{{Src: tmp}}, Network: "none"},
			dir:  tmp,
			want: cat(defaults, []string{"--unshare-net", "--bind", tmp, tmp, "--clearenv", "--chdir", tmp, "--", "agent", "-x"}),
		},
		{
			name: "bad network",
			s:    Sandbox{Bwrap: "/bin/bwrap", RW: []Mount{{Src: tmp}}, Network: "vpn"},
			dir:  tmp,
			err:  "network",
		},
		{
			name: "bad backend",
			s:    Sandbox{Backend: "docker", Bwrap: "/bin/bwrap", RW: []Mount{{Src: tmp}}},
			dir:  tmp,
			err:  "backend",
		},
		{
			name: "env keep",
			s:    Sandbox{Bwrap: "/bin/bwrap", RW: []Mount{{Src: tmp}}, EnvKeep: []string{"ACPHOST_KEEP", "ACPHOST_UNSET_XYZ"}},
			dir:  tmp,
			want: cat(defaults, []string{"--bind", tmp, tmp, "--clearenv", "--setenv", "ACPHOST_KEEP", "v1", "--chdir", tmp, "--", "agent", "-x"}),
		},
		{
			name: "dir outside rw",
			s:    Sandbox{Bwrap: "/bin/bwrap", RW: []Mount{{Src: tmp, Dst: work}}},
			dir:  work + "x",
			err:  "not inside",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Args(tt.s, "agent", []string{"-x"}, tt.dir)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want containing %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestSandboxJSON(t *testing.T) {
	in := `{"ro":["/nix",{"src":"/etc/x","dst":"/etc/y","skip_missing":true}],"rw":["/w"],"network":"none"}`
	var s Sandbox
	if err := json.Unmarshal([]byte(in), &s); err != nil {
		t.Fatal(err)
	}
	want := Sandbox{
		RO:      []Mount{{Src: "/nix"}, {Src: "/etc/x", Dst: "/etc/y", SkipMissing: true}},
		RW:      []Mount{{Src: "/w"}},
		Network: "none",
	}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("got %+v", s)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back Sandbox
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, want) {
		t.Fatalf("round trip got %+v", back)
	}
}

func TestSandboxPastaArgs(t *testing.T) {
	tmp := t.TempDir()
	base := Sandbox{Bwrap: "/bin/bwrap", Pasta: "/bin/pasta", RW: []Mount{{Src: tmp}}, Network: "pasta"}
	pastaArgv := []string{"/bin/pasta", "--config-net", "--foreground", "--quiet", "-t", "none", "-u", "none", "-T", "none", "-U", "none", "--"}
	bw := []string{
		"/bin/bwrap", "--die-with-parent", "--unshare-pid", "--new-session",
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--cap-drop", "ALL",
		"--bind", tmp, tmp, "--clearenv", "--chdir", tmp, "--", "agent",
	}

	got, err := Args(base, "agent", nil, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if want := slices.Concat(pastaArgv, bw); !slices.Equal(got, want) {
		t.Fatalf("no deny:\n got %q\nwant %q", got, want)
	}

	if _, err := exec.LookPath("nft"); err == nil {
		s := base
		s.Deny = []string{"10.1.2.3/8", "fd00::/8", "192.0.2.1/32"}
		got, err := Args(s, "agent", nil, tmp)
		if err != nil {
			t.Fatal(err)
		}
		rules := got[len(pastaArgv)+5]
		want := "table inet acphost { chain out { type filter hook output priority 0; policy accept; ip daddr { 10.0.0.0/8, 192.0.2.1/32 } reject; ip6 daddr { fd00::/8 } reject; }; }"
		if rules != want {
			t.Fatalf("rules %q", rules)
		}
		if !slices.Equal(got[len(got)-len(bw):], bw) {
			t.Fatalf("bwrap argv %q", got)
		}
	}
}

func TestSandboxDenyValidation(t *testing.T) {
	tmp := t.TempDir()
	for _, tc := range []struct {
		name string
		s    Sandbox
		err  string
	}{
		{"deny with host", Sandbox{Network: "host", Deny: []string{"10.0.0.0/8"}}, "deny needs network pasta"},
		{"deny with default", Sandbox{Deny: []string{"10.0.0.0/8"}}, "deny needs network pasta"},
		{"deny with none", Sandbox{Network: "none", Deny: []string{"10.0.0.0/8"}}, "deny needs network pasta"},
		{"bad cidr", Sandbox{Network: "pasta", Deny: []string{"10.0.0.0"}}, "deny"},
		{"bad cidr text", Sandbox{Network: "pasta", Deny: []string{"nope/8"}}, "deny"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.s.Bwrap = "/bin/bwrap"
			tc.s.RW = []Mount{{Src: tmp}}
			_, err := Args(tc.s, "agent", nil, tmp)
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("got %v, want %q", err, tc.err)
			}
		})
	}
}
