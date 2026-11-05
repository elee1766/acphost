package acphost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Alloc is a block of Size outside uids starting at UID and Size outside
// gids starting at GID, held by a lock file until Release. Use User to
// map a session to it.
type Alloc struct {
	UID, GID, Size int

	f *os.File
}

// User returns a User that runs as uid:gid inside and maps to the first
// id of the block outside.
func (a *Alloc) User(uid, gid int) User {
	return User{UID: uid, GID: gid, MapUID: a.UID, MapGID: a.GID}
}

// Release frees the block. The lock file stays, so a later allocation
// cannot race with its removal. Safe to call more than once.
func (a *Alloc) Release() error {
	if a == nil || a.f == nil {
		return nil
	}
	f := a.f
	a.f = nil
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return f.Close()
}

// blockStarts cuts each range into whole blocks of size and returns their
// first ids in order.
func blockStarts(rs []Range, size int) []int {
	var out []int
	for _, r := range rs {
		for off := 0; off+size <= r.Count; off += size {
			out = append(out, r.Start+off)
		}
	}
	return out
}

// AllocSubIDs picks a block of size uids inside uids and size gids
// inside gids (ranges as from ParseSubIDs) that no other live holder has.
// Block i of the uid ranges pairs with block i of the gid ranges. Each
// block is held by an flock on a file in lockDir, so holders in any
// process never share one, and a block whose holder died is free again.
// No free block is a PermanentError.
func AllocSubIDs(lockDir string, uids, gids []Range, size int) (*Alloc, error) {
	if size <= 0 {
		return nil, Permanentf("subid alloc: size %d must be positive", size)
	}
	us, gs := blockStarts(uids, size), blockStarts(gids, size)
	n := min(len(us), len(gs))
	if n == 0 {
		return nil, Permanentf("subid alloc: ranges hold no block of %d ids", size)
	}
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return nil, fmt.Errorf("subid alloc: %w", err)
	}
	for i := range n {
		name := filepath.Join(lockDir, fmt.Sprintf("subid-%d-%d-%d.lock", us[i], gs[i], size))
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return nil, fmt.Errorf("subid alloc: %w", err)
		}
		// flock is per open file, so this also excludes other goroutines
		// of this process.
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return &Alloc{UID: us[i], GID: gs[i], Size: size, f: f}, nil
		}
		_ = f.Close()
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("subid alloc: lock %s: %w", name, err)
		}
	}
	return nil, Permanentf("subid alloc: all %d blocks of %d ids are in use", n, size)
}
