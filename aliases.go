package acphost

import (
	"github.com/elee1766/acphost/cgroup"
	"github.com/elee1766/acphost/internal/xerr"
	"github.com/elee1766/acphost/sandbox"
	"github.com/elee1766/acphost/userns"
)

// Aliases for the isolation types and functions that moved to the
// sandbox, cgroup and userns packages (#156). They stay for one release
// so callers can move at their own pace, then go (#160). New code should
// use the packages.

// PermanentError is a spawn failure that retrying cannot fix: the host
// config points at something that is not there (#146).
type PermanentError = xerr.PermanentError

// Permanentf returns a PermanentError.
func Permanentf(format string, a ...any) error { return xerr.Permanentf(format, a...) }

// Sandbox is sandbox.Sandbox.
type Sandbox = sandbox.Sandbox

// DNS is sandbox.DNS.
type DNS = sandbox.DNS

// Mount is sandbox.Mount.
type Mount = sandbox.Mount

// SandboxArgs is sandbox.Args.
func SandboxArgs(s Sandbox, cmd string, args []string, dir string) ([]string, error) {
	return sandbox.Args(s, cmd, args, dir)
}

// CheckSandboxDNS is sandbox.CheckDNS.
func CheckSandboxDNS(s Sandbox, name string) error { return sandbox.CheckDNS(s, name) }

// Cgroup is cgroup.Cgroup.
type Cgroup = cgroup.Cgroup

// CgroupStats is cgroup.Stats.
type CgroupStats = cgroup.Stats

// DelegatedInfo is cgroup.DelegatedInfo.
type DelegatedInfo = cgroup.DelegatedInfo

// ReasonOOM is the exit.json Reason when the cgroup OOM-killed the agent.
const ReasonOOM = cgroup.ReasonOOM

// DelegatedCgroup is cgroup.Delegated.
func DelegatedCgroup() (DelegatedInfo, error) { return cgroup.Delegated() }

// SetupDelegation is cgroup.SetupDelegation.
func SetupDelegation(own string, controllers []string) (string, error) {
	return cgroup.SetupDelegation(own, controllers)
}

// User is userns.User.
type User = userns.User

// Range is userns.Range.
type Range = userns.Range

// Alloc is userns.Alloc.
type Alloc = userns.Alloc

// ParseSubIDs is userns.ParseSubIDs.
var ParseSubIDs = userns.ParseSubIDs

// AllocSubIDs is userns.AllocSubIDs.
var AllocSubIDs = userns.AllocSubIDs
