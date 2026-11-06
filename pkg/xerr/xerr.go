// Package xerr holds the permanent spawn error shared by acphost and its
// isolation packages. Callers use acphost.PermanentError, an alias.
package xerr

import "fmt"

// PermanentError is a spawn failure that retrying cannot fix: the host
// config points at something that is not there (#146).
type PermanentError struct{ Err error }

// Error returns the wrapped error's message.
func (e *PermanentError) Error() string { return e.Err.Error() }

// Unwrap returns the wrapped error.
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanentf returns a PermanentError.
func Permanentf(format string, a ...any) error {
	return &PermanentError{Err: fmt.Errorf(format, a...)}
}
