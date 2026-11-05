package acphost

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// TestSubIDAllocHelper is a child process: it allocates a block in
// SUBID_ALLOC_DIR, prints it, then holds it until stdin closes.
func TestSubIDAllocHelper(t *testing.T) {
	dir := os.Getenv("SUBID_ALLOC_DIR")
	if dir == "" {
		t.Skip("helper process only")
	}
	a, err := AllocSubIDs(dir, []Range{{100000, 4}}, []Range{{200000, 4}}, 2)
	if err != nil {
		fmt.Println("err", err)
		os.Exit(1)
	}
	fmt.Println(a.UID)
	_, _ = os.Stdin.Read(make([]byte, 1))
	os.Exit(0)
}

func TestAllocSubIDsDistinct(t *testing.T) {
	dir := t.TempDir()
	uids, gids := []Range{{100000, 10}}, []Range{{200000, 10}}
	var mu sync.Mutex
	seen := map[int]bool{}
	var allocs []*Alloc
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			a, err := AllocSubIDs(dir, uids, gids, 2)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if seen[a.UID] {
				t.Errorf("uid block %d given twice", a.UID)
			}
			seen[a.UID] = true
			allocs = append(allocs, a)
		})
	}
	wg.Wait()
	if len(allocs) != 5 {
		t.Fatalf("got %d allocs", len(allocs))
	}
	_, err := AllocSubIDs(dir, uids, gids, 2)
	var pe *PermanentError
	if !errors.As(err, &pe) {
		t.Fatalf("exhausted: got %v, want PermanentError", err)
	}
	if err := allocs[2].Release(); err != nil {
		t.Fatal(err)
	}
	_ = allocs[2].Release()
	a, err := AllocSubIDs(dir, uids, gids, 2)
	if err != nil {
		t.Fatal(err)
	}
	if a.UID != allocs[2].UID || a.GID != allocs[2].GID {
		t.Fatalf("after release got %d/%d, want %d/%d", a.UID, a.GID, allocs[2].UID, allocs[2].GID)
	}
	if u := a.User(0, 0); u.MapUID != a.UID || u.MapGID != a.GID {
		t.Fatalf("User: %+v", u)
	}
}

func TestAllocSubIDsBadInput(t *testing.T) {
	var pe *PermanentError
	if _, err := AllocSubIDs(t.TempDir(), []Range{{1, 1}}, []Range{{1, 1}}, 2); !errors.As(err, &pe) {
		t.Fatalf("range smaller than size: %v", err)
	}
	if _, err := AllocSubIDs(t.TempDir(), []Range{{1, 1}}, []Range{{1, 1}}, 0); !errors.As(err, &pe) {
		t.Fatalf("zero size: %v", err)
	}
}

func startHolder(t *testing.T, dir string) (*exec.Cmd, func(), int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSubIDAllocHelper$")
	cmd.Env = append(os.Environ(), "SUBID_ALLOC_DIR="+dir)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, _ := out.Read(buf)
	var uid int
	if _, err := fmt.Sscan(strings.TrimSpace(string(buf[:n])), &uid); err != nil {
		t.Fatalf("helper said %q", buf[:n])
	}
	return cmd, func() { _ = in.Close() }, uid
}

func TestAllocSubIDsAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	cmd, stop, child := startHolder(t, dir)
	uids, gids := []Range{{100000, 4}}, []Range{{200000, 4}}
	a, err := AllocSubIDs(dir, uids, gids, 2)
	if err != nil {
		t.Fatal(err)
	}
	if a.UID == child {
		t.Fatalf("parent and child share block %d", child)
	}
	if _, err := AllocSubIDs(dir, uids, gids, 2); err == nil {
		t.Fatal("want exhausted while child holds a block")
	}
	// The child dies without Release: its block is free again.
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	stop()
	b, err := AllocSubIDs(dir, uids, gids, 2)
	if err != nil {
		t.Fatalf("dead holder's block not reused: %v", err)
	}
	if b.UID != child {
		t.Fatalf("got %d, want dead holder's %d", b.UID, child)
	}
}
