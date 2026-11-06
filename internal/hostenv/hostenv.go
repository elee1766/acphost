// Package hostenv holds host lookups shared by acphost and its isolation
// packages. Variables so tests can point them elsewhere.
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
