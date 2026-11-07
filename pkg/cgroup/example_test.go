package cgroup_test

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/elee1766/acphost/pkg/cgroup"
)

// None of these examples has an "Output:" line, so go test compiles them
// but does not run them: they need a delegated cgroup v2 subtree or a
// systemd manager, which CI runners and most dev shells lack.

// Delegated finds the cgroup this process runs in and whether it may make
// children there. Under a systemd unit with Delegate=yes it is writable.
func ExampleDelegated() {
	d, err := cgroup.Delegated()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(d.Path, d.Writable, d.Controllers)
}

// SetupDelegation prepares a delegated cgroup that holds the caller:
// it moves this process into <own>/controller, enables the controllers
// for children, and returns <own>/sessions. Use that as Cgroup.Parent.
// Moving the caller out first is what the cgroup v2 "no internal
// processes" rule needs before controllers can be enabled.
func ExampleSetupDelegation() {
	d, err := cgroup.Delegated()
	if err != nil || !d.Writable {
		fmt.Println("no delegated cgroup:", err)
		return
	}
	cg := cgroup.Cgroup{MemoryMax: "2G", PidsMax: 512}
	parent, err := cgroup.SetupDelegation(d.Path, cg.Controllers())
	if err != nil {
		fmt.Println(err)
		return
	}
	cg.Parent = parent // session cgroups go under <own>/sessions
	fmt.Println(cg.Parent)
}

// Prepare makes <Parent>/<session> with the limits set (the cgroupfs
// driver). The caller starts the command in it (proc.Start does, with
// clone3 and a cgroup fd), reads usage, then ends it: Kill writes
// cgroup.kill, WaitEmpty waits until the last process is gone, and
// Remove deletes the directory, which cgroupfs refuses while a process
// remains.
func ExamplePrepare() {
	d, err := cgroup.Delegated()
	if err != nil || !d.Writable {
		fmt.Println("no delegated cgroup:", err)
		return
	}
	cg := cgroup.Cgroup{
		Parent:    d.Path, // or the sessions dir from SetupDelegation
		MemoryMax: "2G",
		SwapMax:   "0", // make MemoryMax a hard cap
		PidsMax:   512,
	}
	dir, limits, err := cgroup.Prepare(cg, "session-1")
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, l := range limits {
		fmt.Println(l.File, l.Value) // what was written, in order
	}

	// ... start the command in dir, wait for it ...

	st := cgroup.ReadStats(dir) // read before the cgroup is removed
	fmt.Println(st.MemoryPeak, st.CPUUsageUsec, st.OOMKills)

	cgroup.Kill(dir)                           // SIGKILL everything left
	cgroup.WaitEmpty(dir, 5*time.Second)       // populated 0
	if err := cgroup.Remove(dir); err != nil { // only once empty
		fmt.Println(err)
	}
}

// CheckManager says whether the systemd driver can work here: it
// connects to the manager's private socket (system manager as root, else
// the user manager) and asks for its version. Preflight runs it for specs
// with Driver systemd.
func ExampleCheckManager() {
	if err := cgroup.CheckManager(); err != nil {
		fmt.Println("systemd driver unavailable:", err)
		return
	}
	fmt.Println("systemd manager answers on", cgroup.ManagerSocket())
}

// StartScope is the systemd driver: Parent is a slice name, and the
// manager makes a transient scope that adopts a pid and applies the
// limits as unit properties. The caller enables no controllers, so its
// own unit can restart without touching live sessions. The scope is
// removed by systemd once it is empty; there is no stop call. proc.Start
// does this for Spec.Cgroup with Driver systemd: it adopts a holder
// process first and starts the command inside the scope with clone3.
func ExampleStartScope() {
	// Any process of the caller's uid will do to hold the scope open.
	holder := exec.Command("sleep", "600")
	if err := holder.Start(); err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = holder.Process.Kill(); _ = holder.Wait() }()

	cg := cgroup.Cgroup{
		Driver:    cgroup.DriverSystemd,
		Parent:    "acphost.slice", // empty: the manager's default slice
		MemoryMax: "2G",
		PidsMax:   512,
	}
	sc, limits, err := cgroup.StartScope(context.Background(), cg, "session-1", holder.Process.Pid)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(sc.Unit, sc.Dir) // acphost-session-1-<pid>.scope and its cgroup dir
	for _, l := range limits {
		fmt.Println(l.File, l.Value) // as the kernel has them (CPU quota is rounded)
	}
}

// StartHostScope puts a detached host in its own transient scope, outside
// the caller's unit, with no limits and no delegation. acphost.InScope
// does this for StartDetached.
func ExampleStartHostScope() {
	holder := exec.Command("sleep", "600")
	if err := holder.Start(); err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = holder.Process.Kill(); _ = holder.Wait() }()

	sc, err := cgroup.StartHostScope(context.Background(), "acphost.slice", "session-1", holder.Process.Pid)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(sc.Unit, sc.Dir) // acphost-host-session-1-<pid>.scope
}
