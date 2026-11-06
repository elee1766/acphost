// Package proc starts commands with acphost's isolation (sandbox,
// cgroup, user namespace, parent-death kill). See #155.
//
// Some isolation re-execs the calling binary as a small helper, so a
// program that uses acphost must call MaybeChild as the first line of
// main (and of TestMain in tests).
package proc

import "github.com/elee1766/acphost/internal/reexec"

// MaybeChild runs an acphost helper and does not return when this
// process was started as one (picked by argv, with its input on an
// inherited fd). Otherwise it returns at once. Call it first in main,
// before flags or other setup. Without it, starting a command that needs
// a helper fails with ErrNoMaybeChild.
func MaybeChild() { reexec.MaybeChild() }

// ErrNoMaybeChild is returned when isolation needs a helper re-exec and
// the program did not call MaybeChild.
var ErrNoMaybeChild = reexec.ErrNotCalled
