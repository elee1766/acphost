package acphosttest_test

import (
	"os"
	"testing"

	"github.com/elee1766/acphost/pkg/proc"
)

func TestMain(m *testing.M) {
	proc.MaybeChild()
	os.Exit(m.Run())
}
