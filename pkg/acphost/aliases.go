package acphost

import "github.com/gfx-labs/acphost/pkg/xerr"

// PermanentError is a spawn failure that retrying cannot fix: the host
// config points at something that is not there (#146). It is xerr's type,
// so the isolation packages return the same error. Permanent tells it
// from a transient failure.
type PermanentError = xerr.PermanentError

// Permanentf returns a PermanentError.
func Permanentf(format string, a ...any) error { return xerr.Permanentf(format, a...) }
