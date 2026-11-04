package acphost

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

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
			got, err := SandboxArgs(tt.s, "agent", []string{"-x"}, tt.dir)
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
	b, err := json.Marshal(Spec{Sandbox: &s})
	if err != nil {
		t.Fatal(err)
	}
	var back Spec
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*back.Sandbox, want) {
		t.Fatalf("round trip got %+v", *back.Sandbox)
	}
}
