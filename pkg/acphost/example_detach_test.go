package acphost_test

import (
	"context"
	"fmt"
	"os"

	"github.com/elee1766/acphost/pkg/acphost"
)

// StartDetached runs a host that outlives the caller; proc.Open finds it
// again after a restart. The program calls acphost.MaybeChild first in
// main. No Output line: it needs an agent installed.
func ExampleStartDetached() {
	dir, _ := os.MkdirTemp("", "acphost-detached")
	_ = acphost.WriteSpec(dir, acphost.Spec{
		Protocol: acphost.Protocol, Session: "demo",
		Command: "claude-code-acp", Dir: "/path/to/work", LeaseMS: 600_000,
	})
	// Returns once the agent started; a spawn failure is a *SpawnError.
	d, err := acphost.StartDetached(dir, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	nc, _, _, err := acphost.Connect(context.Background(), dir)
	if err == nil {
		_ = nc.Close() // speak ACP on nc
	}

	// Later, possibly from another process: Running, Stop and Wait as
	// for any proc.Detached. Wait returns the agent's exit code, signal
	// and cgroup stats; ReadExit adds the host's reason.
	if d.Running() {
		_ = d.Stop()
	}
	ex, _ := d.Wait(context.Background())
	hex, _ := acphost.ReadExit(dir)
	fmt.Println(ex.ExitCode, hex.Reason)
}

// InScope puts the host in a transient systemd scope that the manager
// owns. Choose it when the caller runs as a systemd unit that restarts:
// the host is then outside the unit's cgroup, so the unit empties when it
// stops and the next start does not meet EBUSY (note 240). It needs the
// manager the systemd cgroup driver needs; with a user manager that means
// the caller runs inside it (loginctl enable-linger). Not with InCgroup.
// No Output line: it needs a systemd manager and an agent.
func ExampleInScope() {
	dir, _ := os.MkdirTemp("", "acphost-scope")
	_ = acphost.WriteSpec(dir, acphost.Spec{
		Protocol: acphost.Protocol, Session: "demo",
		Command: "claude-code-acp", Dir: "/path/to/work", LeaseMS: 600_000,
	})
	// The host runs in acphost-host-demo-<pid>.scope under acphost.slice
	// (empty slice: the manager's default). systemd removes the scope
	// once the host and what it forked have exited.
	d, err := acphost.StartDetached(dir, nil, acphost.InScope("acphost.slice"))
	if err != nil {
		// A bad slice name or no manager fails here, before the host
		// starts; Preflight reports the same gaps ahead of time.
		fmt.Println(err)
		return
	}
	fmt.Println(d.Pid)
}

// InCgroup puts the host in a cgroup v2 directory the caller already
// owns. Choose it with delegated cgroupfs and no systemd manager to ask:
// a unit with Delegate=yes, a container, or root. The host then counts
// against that directory's limits, not the caller's own (note 223).
// acphost does not create or remove the directory, and many hosts can
// share it. It is not Spec.Cgroup, which limits the agent and can be used
// alongside. No Output line: it needs a delegated cgroup and an agent.
func ExampleInCgroup() {
	dir, _ := os.MkdirTemp("", "acphost-incgroup")
	_ = acphost.WriteSpec(dir, acphost.Spec{
		Protocol: acphost.Protocol, Session: "demo",
		Command: "claude-code-acp", Dir: "/path/to/work", LeaseMS: 600_000,
	})
	// hosts is writable by the caller (its cgroup.procs). A directory
	// that cannot be opened fails StartDetached before anything starts.
	hosts := "/sys/fs/cgroup/myservice/hosts"
	d, err := acphost.StartDetached(dir, nil, acphost.InCgroup(hosts))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(d.Pid)
}
