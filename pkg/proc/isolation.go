package proc

import (
	"strings"

	"github.com/elee1766/acphost/pkg/cgroup"
	"github.com/elee1766/acphost/pkg/sandbox"
)

// Isolation is what Start actually applied, the content of an ACP
// host's isolation.json. Each part is nil when that feature was not
// used.
type Isolation struct {
	Cgroup  *CgroupApplied  `json:"cgroup,omitempty"`
	User    *UserApplied    `json:"user,omitempty"`
	Network *NetworkApplied `json:"network,omitempty"`
	// Join maps each namespace type joined (Spec.Join) to its path and
	// the namespace it resolved to at start.
	Join map[string]JoinApplied `json:"join,omitempty"`
	// Bwrap is the full argv run when sandboxed, including any pasta
	// prefix.
	Bwrap []string `json:"bwrap,omitempty"`
	// PdeathHelper is the argv prefix of the pdeath helper run in front
	// of Bwrap with network pasta (#76). The process chain is
	// PdeathHelper, then Bwrap (pasta, then bwrap). Nil without pasta.
	PdeathHelper []string `json:"pdeath_helper,omitempty"`
}

// CgroupApplied is the command's cgroup and the limit files written to it.
// With the systemd driver Dir is the scope's cgroup, Unit its unit name,
// and Limits the values the kernel has once systemd applied them.
type CgroupApplied struct {
	Dir    string         `json:"dir"`
	Driver string         `json:"driver,omitempty"`
	Unit   string         `json:"unit,omitempty"`
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
// forward, the file bound over /etc/resolv.conf, and whether the
// command started under pasta.
type NetworkApplied = sandbox.NetworkApplied

// mapLines turns newuidmap/newgidmap arguments (triples) into map lines.
func mapLines(args []string) []string {
	var l []string
	for i := 0; i+2 < len(args); i += 3 {
		l = append(l, strings.Join(args[i:i+3], " "))
	}
	return l
}
