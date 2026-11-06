package acphost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elee1766/acphost/cgroup"
)

func TestOOMReason(t *testing.T) {
	dir := t.TempDir()
	ev := filepath.Join(dir, "memory.events")
	write := func(n string) {
		if err := os.WriteFile(ev, []byte("low 0\nhigh 0\nmax 3\noom 1\noom_kill "+n+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	end := func() int64 { return cgroup.ReadStats(dir).OOMKills }

	// Missing file: normal reason.
	start := cgroup.OOMKills(dir)
	if got := cgroup.OOMReason("agent exited", start, end()); got != "agent exited" {
		t.Fatalf("missing file: %q", got)
	}

	// Count unchanged (an earlier kill does not count): normal reason.
	write("2")
	start = cgroup.OOMKills(dir)
	if got := cgroup.OOMReason("agent exited", start, end()); got != "agent exited" {
		t.Fatalf("unchanged: %q", got)
	}

	// Count rose: ReasonOOM.
	write("3")
	if got := cgroup.OOMReason("agent exited", start, end()); got != ReasonOOM {
		t.Fatalf("rose: %q, want %q", got, ReasonOOM)
	}
}
