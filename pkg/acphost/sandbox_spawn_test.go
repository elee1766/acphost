package acphost

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// CheckSpawn reports a missing pasta as permanent, like a missing bwrap.
func TestCheckSpawnMissingPasta(t *testing.T) {
	tmp := t.TempDir()
	s := Spec{Command: "agent", Dir: tmp, Sandbox: &Sandbox{
		Bwrap: "/bin/sh", RW: []Mount{{Src: tmp}}, Network: "pasta", Pasta: filepath.Join(tmp, "nopasta"),
	}}
	var pe *PermanentError
	if err := CheckSpawn(s, false); !errors.As(err, &pe) || !strings.Contains(err.Error(), "pasta") {
		t.Fatalf("missing pasta: got %v", err)
	}
	s.Sandbox.Pasta = ""
	t.Setenv("PATH", tmp)
	if err := CheckSpawn(s, false); !errors.As(err, &pe) || !strings.Contains(err.Error(), "pasta") {
		t.Fatalf("pasta not on PATH: got %v", err)
	}
}

func TestSandboxSpecJSON(t *testing.T) {
	want := Sandbox{RO: []Mount{{Src: "/nix"}}, Network: "none"}
	b, err := json.Marshal(Spec{Sandbox: &want})
	if err != nil {
		t.Fatal(err)
	}
	var back Spec
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*back.Sandbox, want) {
		t.Fatalf("round trip got %+v", *back.Sandbox)
	}
}
