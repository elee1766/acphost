package acphost

import "github.com/elee1766/acphost/pkg/xerr"

// The isolation aliases (Sandbox, Cgroup, User and friends) were removed
// in #160; use the sandbox, cgroup and userns packages.

// PermanentError is a spawn failure that retrying cannot fix: the host
// config points at something that is not there (#146).
type PermanentError = xerr.PermanentError

// Permanentf returns a PermanentError.
func Permanentf(format string, a ...any) error { return xerr.Permanentf(format, a...) }
