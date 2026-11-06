// Package trace records helper lookups and cgroupfs or subordinate id
// accesses for tests. Used by acphost itself; not a stable API.
package trace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// Hook is told, when set, is told about every helper lookup or run and every
// cgroupfs or subordinate id access. Tests use it to check that a spec
// with the isolation features unset touches none of them. Atomic because
// host goroutines may call it while a test swaps it.
var Hook atomic.Pointer[func(string)]

// Event reports what to Hook.
func Event(what string) {
	if f := Hook.Load(); f != nil {
		(*f)(what)
	}
}

// LookPath is exec.LookPath, traced.
func LookPath(name string) (string, error) {
	Event("look:" + name)
	return exec.LookPath(name)
}

// LookCommand checks cmd resolves to an executable, using the PATH in
// env when set.
func LookCommand(cmd string, env []string) error {
	Event("look:" + cmd)
	if strings.Contains(cmd, "/") {
		st, err := os.Stat(cmd)
		if err != nil {
			return err
		}
		if st.IsDir() || st.Mode()&0o111 == 0 {
			return fmt.Errorf("not executable: %w", exec.ErrNotFound)
		}
		return nil
	}
	path := os.Getenv("PATH")
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	for _, d := range filepath.SplitList(path) {
		if d == "" {
			d = "."
		}
		if st, err := os.Stat(filepath.Join(d, cmd)); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return nil
		}
	}
	return exec.ErrNotFound
}
