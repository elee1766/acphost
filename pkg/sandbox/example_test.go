package sandbox_test

import (
	"fmt"

	"github.com/elee1766/acphost/pkg/sandbox"
)

// An overlay merges host dirs onto one path in the sandbox, lowest layer
// first. Mode "tmp" accepts writes and drops them when the command ends.
// Args returns the bwrap argv without running anything; it checks that
// the layers exist.
func ExampleOverlay() {
	sb := sandbox.Sandbox{
		RO: []sandbox.Mount{{Src: "/nix/store"}},
		RW: []sandbox.Mount{{Src: "/home/me/work"}},
		Overlay: []sandbox.Overlay{{
			Dst:    "/opt/tools",
			Layers: []string{"/srv/tools/base", "/srv/tools/extra"},
			Mode:   "tmp",
		}},
		Network: "none",
	}
	argv, err := sandbox.Args(sb, "agent", nil, "/home/me/work")
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(argv)
}
