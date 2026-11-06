package acphost

import (
	"errors"
	"os"
	"path/filepath"
)

// spawnFailure returns a SpawnError when dir holds an exit.json with
// ReasonSpawnFailed that is newer than spec.json and no host holds the
// lock, so a client that cannot reach the socket learns the host gave
// up instead of retrying until its own timeout.
func spawnFailure(dir string) error {
	ex, ok := ReadExit(dir)
	if !ok || ex.Reason != ReasonSpawnFailed {
		return nil
	}
	es, err1 := os.Stat(filepath.Join(dir, ExitFile))
	ss, err2 := os.Stat(filepath.Join(dir, SpecFile))
	if err1 != nil || err2 != nil || es.ModTime().Before(ss.ModTime()) {
		return nil // exit.json from a run before this spec
	}
	if Alive(dir) {
		return nil
	}
	msg := ex.Error
	if msg == "" {
		msg = "host gave no reason"
	}
	return &SpawnError{Exit: ex, Err: errors.New(msg)}
}
