package proc

import "testing"

// Temporary: proves the failed-tests artifact (#178). Reverted before merge.
func TestCI178Fails(t *testing.T) { t.Fatal("deliberate failure for #178") }
