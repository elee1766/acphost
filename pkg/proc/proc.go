// Package proc runs a command with acphost's isolation (sandbox, cgroup,
// user namespace, namespace joins, parent-death kill) and no ACP, socket
// or journal.
//
// [Start] runs it attached to the caller: it dies with the caller or its
// context, and [Process].Wait cleans up its cgroup. [StartDetached] runs
// it under a supervisor that outlives the caller, and [Open] finds it
// again by its state dir. The ACP host in pkg/acphost is built on the
// same supervisor ([Supervise], [Detach]).
//
// Some isolation re-execs the calling binary as a small helper, so a
// program that uses acphost must call [MaybeChild] as the first line of
// main (and of TestMain in tests), or acphost.MaybeChild when it starts
// ACP hosts.
package proc

import (
	"os"

	"github.com/elee1766/acphost/pkg/reexec"
)

// MaybeChild runs an acphost helper and does not return when this
// process was started as one (picked by argv, with its input on an
// inherited fd). Otherwise it returns at once. Call it first in main,
// before flags or other setup. Without it, starting a command that needs
// a helper fails with ErrNoMaybeChild.
func MaybeChild() {
	// The supervisor lives here, not in reexec, because it runs Start.
	if len(os.Args) > 0 && os.Args[0] == superviseArgv0 {
		reexec.SetCalled(true)
		superviseChild()
	}
	reexec.MaybeChild()
}

// ErrNoMaybeChild is returned when isolation needs a helper re-exec and
// the program did not call MaybeChild.
var ErrNoMaybeChild = reexec.ErrNotCalled
