package acphost_test

import (
	"fmt"

	"github.com/gfx-labs/acphost/pkg/acphost"
)

// Pin creates namespaces and keeps them alive, so several sessions can
// join one network namespace through Spec.Join. CheckSpawn validates the
// paths; JoinPlan lists them in join order. Needs proc.MaybeChild in main
// and util-linux nsenter on PATH.
func ExamplePin() {
	pinned, err := acphost.Pin("net")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = pinned.Close() }()

	spec := acphost.Spec{
		Protocol: acphost.Protocol, Session: "s1", Command: "agent", Dir: "/tmp",
		Join: map[string]string{"user": pinned.Paths["user"], "net": pinned.Paths["net"]},
	}
	if err := acphost.CheckSpawn(spec, false); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(acphost.JoinPlan(spec))
}
