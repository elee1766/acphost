package acphost

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/elee1766/acphost/cgroup"
	"github.com/elee1766/acphost/sandbox"
)

// Isolation is isolation.json: what the host actually applied when it
// started the agent. Each part is nil when that feature was not used, so
// a record with every part nil means none was applied, while a missing
// file means a host too old to write one.
type Isolation struct {
	Cgroup  *CgroupApplied  `json:"cgroup,omitempty"`
	User    *UserApplied    `json:"user,omitempty"`
	Network *NetworkApplied `json:"network,omitempty"`
	// Join maps each namespace type the agent joined (Spec.Join) to its
	// path and the namespace it resolved to when the host started.
	Join map[string]JoinApplied `json:"join,omitempty"`
	// Bwrap is the full argv the host ran when sandboxed, including any
	// pasta prefix.
	Bwrap []string `json:"bwrap,omitempty"`
	// PdeathHelper is the argv prefix of the pdeath helper the host runs
	// in front of Bwrap with network pasta (#76). The process chain is
	// PdeathHelper, then Bwrap (pasta, then bwrap). Nil without pasta.
	PdeathHelper []string `json:"pdeath_helper,omitempty"`
}

// CgroupApplied is the session cgroup and the limit files written to it.
type CgroupApplied struct {
	Dir    string         `json:"dir"`
	Limits []LimitApplied `json:"limits,omitempty"`
}

// LimitApplied is one cgroup limit file and the value written to it.
type LimitApplied = cgroup.LimitApplied

// UserApplied holds the uid_map and gid_map lines ("inside outside
// count") passed to newuidmap and newgidmap.
type UserApplied struct {
	UIDMap []string `json:"uid_map"`
	GIDMap []string `json:"gid_map"`
}

// NetworkApplied is the sandbox network mode ("host", "none" or
// "pasta"), the normalized deny CIDRs loaded as rules, pasta's DNS
// forward, the file bound over /etc/resolv.conf, and whether the agent
// started under pasta.
type NetworkApplied = sandbox.NetworkApplied

// mapLines turns newuidmap/newgidmap arguments (triples) into map lines.
func mapLines(args []string) []string {
	var l []string
	for i := 0; i+2 < len(args); i += 3 {
		l = append(l, strings.Join(args[i:i+3], " "))
	}
	return l
}

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

// writeFileAtomic writes b to path through a temp file and rename, so a
// reader never sees a partial file.
func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".tmp" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
