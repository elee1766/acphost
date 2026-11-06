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
