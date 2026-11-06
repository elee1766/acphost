package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestOverlayArgs(t *testing.T) {
	tmp := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(tmp, name)
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	l1, l2, upper, work := mk("l1"), mk("l2"), mk("upper"), mk("work")
	head := []string{
		"/bin/bwrap", "--die-with-parent", "--unshare-pid", "--new-session",
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp",
	}
	tail := []string{"--clearenv", "--chdir", tmp, "--", "agent"}
	for _, tc := range []struct {
		name string
		o    []Overlay
		want []string
		err  string
	}{
		{
			name: "ro", o: []Overlay{{Dst: "/a/b", Layers: []string{l1, l2}}},
			want: []string{"--overlay-src", l1, "--overlay-src", l2, "--ro-overlay", "/a/b"},
		},
		{
			name: "tmp", o: []Overlay{{Dst: "/a/b", Layers: []string{l1}, Mode: "tmp"}},
			want: []string{"--overlay-src", l1, "--tmp-overlay", "/a/b"},
		},
		{
			name: "persist", o: []Overlay{{Dst: "/a/b", Layers: []string{l1}, Mode: "persist", Upper: upper, Work: work}},
			want: []string{"--overlay-src", l1, "--overlay", upper, work, "/a/b"},
		},
		{name: "no layers", o: []Overlay{{Dst: "/a"}}, err: "no layers"},
		{name: "missing layer", o: []Overlay{{Dst: "/a", Layers: []string{filepath.Join(tmp, "nope")}}}, err: "no such file"},
		{name: "relative layer", o: []Overlay{{Dst: "/a", Layers: []string{"l1"}}}, err: "not absolute"},
		{name: "relative dst", o: []Overlay{{Dst: "a", Layers: []string{l1}}}, err: "dst must be absolute"},
		{name: "too many", o: []Overlay{{Dst: "/a", Layers: slices.Repeat([]string{l1}, MaxOverlayLayers+1)}}, err: "over the limit"},
		{name: "bad mode", o: []Overlay{{Dst: "/a", Layers: []string{l1}, Mode: "rw"}}, err: "unknown mode"},
		{name: "upper without persist", o: []Overlay{{Dst: "/a", Layers: []string{l1}, Upper: upper}}, err: "need mode persist"},
		{name: "upper in layer", o: []Overlay{{Dst: "/a", Layers: []string{tmp}, Mode: "persist", Upper: upper, Work: work}}, err: "inside layer"},
		{name: "work not empty", o: []Overlay{{Dst: "/a", Layers: []string{l1}, Mode: "persist", Upper: upper, Work: tmp}}, err: "nest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Args(Sandbox{Bwrap: "/bin/bwrap", RW: []Mount{{Src: tmp}}, Overlay: tc.o}, "agent", nil, tmp)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := slices.Concat(head, []string{"--bind", tmp, tmp}, tc.want, tail)
			if pathDepth(tmp) > 2 {
				want = slices.Concat(head, tc.want, []string{"--bind", tmp, tmp}, tail)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("got  %q\nwant %q", got, want)
			}
		})
	}

	// Parents first: an overlay on / goes before a deeper bind, one
	// deeper than the bind after it.
	got, err := Args(Sandbox{
		Bwrap: "/bin/bwrap", RW: []Mount{{Src: tmp, Dst: "/w"}},
		Overlay: []Overlay{{Dst: "/w/x/y", Layers: []string{l1}}, {Dst: "/", Layers: []string{l2}}},
	}, "agent", nil, "/w")
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Concat(head, []string{
		"--overlay-src", l2, "--ro-overlay", "/",
		"--bind", tmp, "/w",
		"--overlay-src", l1, "--ro-overlay", "/w/x/y",
		"--clearenv", "--chdir", "/w", "--", "agent",
	})
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}

	// Work must be empty.
	if err := os.WriteFile(filepath.Join(work, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Args(Sandbox{
		Bwrap: "/bin/bwrap", RW: []Mount{{Src: tmp}},
		Overlay: []Overlay{{Dst: "/a", Layers: []string{l1}, Mode: "persist", Upper: upper, Work: work}},
	}, "agent", nil, tmp)
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("err %v, want not empty", err)
	}
}

// Needs bwrap with overlay support (0.10+) and user namespaces.
func TestOverlayBwrap(t *testing.T) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not on PATH")
	}
	probe := t.TempDir()
	if out, err := exec.Command(bwrap, "--ro-bind", "/", "/", "--overlay-src", probe, "--tmp-overlay", "/tmp", "true").CombinedOutput(); err != nil {
		t.Skipf("bwrap overlays do not work here: %v: %s", err, out)
	}
	tmp := t.TempDir()
	var layers []string
	for i, v := range []string{"one", "two", "three"} {
		d := filepath.Join(tmp, "l"+string(rune('0'+i)))
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "f"), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "only"+v), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		layers = append(layers, d)
	}
	m := filepath.Join(tmp, "merged")
	run := func(mode, script string) string {
		t.Helper()
		argv, err := Args(Sandbox{
			Bwrap:   bwrap,
			RO:      []Mount{{Src: "/"}},
			RW:      []Mount{{Src: tmp}},
			Overlay: []Overlay{{Dst: m, Layers: layers, Mode: mode}},
			EnvKeep: []string{"PATH"},
		}, "sh", []string{"-c", strings.ReplaceAll(script, "/merged", m)}, tmp)
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got := run("ro", "cat /merged/f; ls /merged | tr '\\n' ' '"); got != "threef onlyone onlythree onlytwo" {
		t.Fatalf("ro: %q", got)
	}
	if got := run("tmp", "echo new > /merged/f && echo x > /merged/added && cat /merged/f"); got != "new" {
		t.Fatalf("tmp: %q", got)
	}
	for i, v := range []string{"one", "two", "three"} {
		b, err := os.ReadFile(filepath.Join(layers[i], "f"))
		if err != nil || string(b) != v {
			t.Fatalf("layer %d changed: %q %v", i, b, err)
		}
		if _, err := os.Stat(filepath.Join(layers[i], "added")); err == nil {
			t.Fatalf("layer %d got the write", i)
		}
	}
}
