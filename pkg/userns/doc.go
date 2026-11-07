// Package userns describes a mapped user namespace: subuid/subgid
// parsing and allocation, validation and newuidmap/newgidmap arguments.
// [User] is the description: the ids a command runs as inside the
// namespace and the ids they map to outside.
//
// To give each session its own subordinate ids, read the ranges with
// [ReadSubIDs], allocate a block with [AllocSubIDs] and map the session
// with [Alloc].User. [Check] validates a [User] before a spawn.
//
// Most callers set [User] on a spec and let pkg/acphost or pkg/proc
// (Start) run it. The helper that enters the namespace re-execs the
// calling binary, so the program must call proc.MaybeChild first in main.
package userns
