package xerr

import "testing"

// Throwaway for #263: fails on purpose so CI uploads failed-tests. Never merge.
func TestThrowaway263Fails(t *testing.T) {
	t.Fatal("deliberate failure for #263")
}

func TestThrowaway263Sub(t *testing.T) {
	t.Run("bad", func(t *testing.T) { t.Fatal("deliberate subtest failure for #263") })
}
