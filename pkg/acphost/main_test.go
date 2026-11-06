package acphost

import (
	"os"
	"testing"
)

// The userns, pdeath and supervisor helpers, and the detached host
// (StartDetached), re-exec this test binary.
func TestMain(m *testing.M) {
	MaybeChild()
	if dir := os.Getenv("ACPHOST_TEST_DETACH_CHILD"); dir != "" {
		detachChild(dir)
	}
	os.Exit(m.Run())
}
