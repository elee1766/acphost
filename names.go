package acphost

import (
	"path/filepath"
	"strings"
)

// MaxSocketPath is the longest unix socket path Linux accepts: sun_path is
// 108 bytes including the trailing NUL.
const MaxSocketPath = 107

// maxNameLen is the longest single path element (NAME_MAX). The session
// name becomes a cgroup directory and part of the GC root link name.
const maxNameLen = 255

// SocketPath returns the path of the host's ACP socket in session dir.
func SocketPath(dir string) string { return filepath.Join(dir, ACPSocket) }

// CheckSocketPath returns a PermanentError if the socket in dir would be
// longer than MaxSocketPath, which otherwise shows up only as a bind
// error after the host has started.
func CheckSocketPath(dir string) error {
	p := SocketPath(dir)
	if len(p) > MaxSocketPath {
		return Permanentf("socket path %s is %d bytes, over the limit of %d (unix sun_path is 108 bytes with the NUL)", p, len(p), MaxSocketPath)
	}
	return nil
}

// checkSession checks that a session name is usable as one path element
// (cgroup dir, GC root link). An empty name is allowed: nothing is
// named after it.
func checkSession(s Spec) error {
	if s.Session == "" {
		return nil
	}
	if s.Session == "." || s.Session == ".." || strings.ContainsAny(s.Session, "/\x00") {
		return Permanentf("bad session name %q: must be a single path element", s.Session)
	}
	prefix := s.GCRootPrefix
	if prefix == "" {
		prefix = DefaultGCRootPrefix
	}
	if n := len(prefix + s.Session); n > maxNameLen {
		return Permanentf("session name %q: GC root link name is %d bytes, over the limit of %d", s.Session, n, maxNameLen)
	}
	return nil
}

// SpawnOption adds checks to CheckSpawn.
type SpawnOption func(*spawnCheck)

type spawnCheck struct{ hostDir string }

// WithHostDir makes CheckSpawn check that the ACP socket in the host's
// session dir fits the unix socket path limit.
func WithHostDir(dir string) SpawnOption { return func(c *spawnCheck) { c.hostDir = dir } }
