// Package cgroup makes, limits, kills and reads a cgroup v2 for a
// command, with one of two drivers chosen by [Cgroup].Driver.
//
// cgroupfs (the default) makes a directory under a delegated parent:
// [Delegated] finds one, [SetupDelegation] prepares it, [Prepare] makes
// the session cgroup and writes the limits, and [ReadStats], [Kill],
// [WaitEmpty] and [Remove] read and end it. systemd asks the manager for
// a transient scope instead: [CheckManager] says whether one answers,
// [StartScope] makes a session's scope and [StartHostScope] a detached
// host's.
//
// Most callers set [Cgroup] on a spec and let pkg/acphost or pkg/proc
// (Start) drive this package.
package cgroup
