package acphost

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/elee1766/acphost/pkg/proc"
)

// Isolation is isolation.json: what the host actually applied when it
// started the agent (proc.Isolation). Each part is nil when that feature
// was not used, so a record with every part nil means none was applied,
// while a missing file means a host too old to write one.
type Isolation = proc.Isolation

// CgroupApplied is proc.CgroupApplied.
type CgroupApplied = proc.CgroupApplied

// LimitApplied is proc.LimitApplied.
type LimitApplied = proc.LimitApplied

// UserApplied is proc.UserApplied.
type UserApplied = proc.UserApplied

// NetworkApplied is proc.NetworkApplied.
type NetworkApplied = proc.NetworkApplied

// ReadIsolation loads isolation.json. It reports false, with no error,
// when the file is absent, as with a host older than this file.
func ReadIsolation(dir string) (Isolation, bool, error) {
	var iso Isolation
	b, err := os.ReadFile(filepath.Join(dir, IsolationFile))
	if errors.Is(err, os.ErrNotExist) {
		return iso, false, nil
	}
	if err != nil {
		return iso, false, err
	}
	if err := json.Unmarshal(b, &iso); err != nil {
		return iso, false, fmt.Errorf("%s: %w", IsolationFile, err)
	}
	return iso, true, nil
}
