package acphost

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOOMReason(t *testing.T) {
	dir := t.TempDir()
	ev := filepath.Join(dir, "memory.events")
	write := func(n string) {
		if err := os.WriteFile(ev, []byte("low 0\nhigh 0\nmax 3\noom 1\noom_kill "+n+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	end := func() int64 { return readCgroupStats(dir).OOMKills }

	// Missing file: normal reason.
	start := oomKills(dir)
	if got := oomReason("agent exited", start, end()); got != "agent exited" {
		t.Fatalf("missing file: %q", got)
	}

	// Count unchanged (an earlier kill does not count): normal reason.
	write("2")
	start = oomKills(dir)
	if got := oomReason("agent exited", start, end()); got != "agent exited" {
		t.Fatalf("unchanged: %q", got)
	}

	// Count rose: ReasonOOM.
	write("3")
	if got := oomReason("agent exited", start, end()); got != ReasonOOM {
		t.Fatalf("rose: %q, want %q", got, ReasonOOM)
	}
}
