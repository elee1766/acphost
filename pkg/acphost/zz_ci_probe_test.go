package acphost

import "testing"

// Deliberate failure to check that CI uploads the failed-tests artifact (#273).
func TestZZCIProbe273(t *testing.T) {
	t.Fatal("deliberate failure for #273")
}
