package acphost

import (
	"os/exec"
	"sync/atomic"
)

// traceHook, when set, is told about every helper lookup or run and every
// cgroupfs or subordinate id access. Tests use it to check that a spec
// with the isolation features unset touches none of them. Atomic because
// host goroutines may call it while a test swaps it.
var traceHook atomic.Pointer[func(string)]

func trace(what string) {
	if f := traceHook.Load(); f != nil {
		(*f)(what)
	}
}

// lookPath is exec.LookPath, traced.
func lookPath(name string) (string, error) {
	trace("look:" + name)
	return exec.LookPath(name)
}
