package acphost

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gfx-labs/acphost/pkg/proc"
)

// Isolation is isolation.json: what the host actually applied when it
// started the agent (proc.Isolation). Each part is nil when that feature
// was not used, so a record with every part nil means none was applied,
// while a missing file means a host too old to write one.
type Isolation = proc.Isolation

// CgroupApplied is the "cgroup" part of Isolation: the session cgroup (or
// systemd scope) and its limit files (proc.CgroupApplied).
type CgroupApplied = proc.CgroupApplied

// LimitApplied is one cgroup limit file and the value in it
// (proc.LimitApplied).
type LimitApplied = proc.LimitApplied

// UserApplied is the "user" part of Isolation: the uid_map and gid_map
// lines passed to newuidmap and newgidmap (proc.UserApplied).
type UserApplied = proc.UserApplied

// NetworkApplied is the "network" part of Isolation: the sandbox network
// mode, deny rules and pasta DNS settings (proc.NetworkApplied).
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
