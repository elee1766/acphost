package acphost

import (
	"os"
	"testing"

	"github.com/elee1766/acphost/pkg/proc"
)

// The userns and pdeath helpers re-exec this test binary.
func TestMain(m *testing.M) {
	proc.MaybeChild()
	os.Exit(m.Run())
}
