package proc_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gfx-labs/acphost/pkg/proc"
	"github.com/gfx-labs/acphost/pkg/sandbox"
)

// Start runs a command attached to the caller with the same isolation the
// host gives agents. Nil Sandbox, Cgroup and User leave each feature off.
func ExampleStart() {
	var out strings.Builder
	p, err := proc.Start(context.Background(), proc.Spec{
		Command: "/bin/sh",
		Args:    []string{"-c", "echo hello"},
	}, proc.Stdout(&out))
	if err != nil {
		fmt.Println(err)
		return
	}
	ex, err := p.Wait()
	fmt.Println(strings.TrimSpace(out.String()), ex.ExitCode, err)
	// Output: hello 0 <nil>
}

// StartDetached runs a command under a supervisor that outlives the
// caller; Open finds it again later, from another process. Both need
// proc.MaybeChild first in main.
func ExampleStartDetached() {
	dir, _ := os.MkdirTemp("", "detached")
	d, err := proc.StartDetached(dir, proc.Spec{
		Command: "agent",
		Sandbox: &sandbox.Sandbox{RW: []sandbox.Mount{{Src: dir}}, Network: "none"},
	}, 10*time.Second)
	if err != nil {
		fmt.Println(err)
		return
	}

	// Later, possibly after a restart: Running probes the supervisor's
	// lock, Wait blocks until exit.json is written.
	d, _ = proc.Open(dir)
	if d.Running() {
		_ = d.Stop()
	}
	ex, _ := d.Wait(context.Background())
	fmt.Println(ex.ExitCode, ex.Signal)
}
