// Package hostenv holds host lookups shared by acphost and its isolation
// packages: the subuid and subgid file paths ([SubUIDPath], [SubGIDPath])
// and the caller's gid lookups. Variables so tests can point them
// elsewhere. Used by acphost itself; not a stable API.
package hostenv

import "os"

// Paths of the subordinate id files.
var (
	SubUIDPath = "/etc/subuid"
	SubGIDPath = "/etc/subgid"
)

// Caller id lookups.
var (
	Getegid   = os.Getegid
	Getgroups = os.Getgroups
)
