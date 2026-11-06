package acphost

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elee1766/acphost/pkg/hostenv"
	"github.com/elee1766/acphost/pkg/userns"
)

func TestCheckSpawnUserBadSubuid(t *testing.T) {
	dir := t.TempDir()
	oldU, oldG := hostenv.SubUIDPath, hostenv.SubGIDPath
	t.Cleanup(func() { hostenv.SubUIDPath, hostenv.SubGIDPath = oldU, oldG })
	hostenv.SubUIDPath = filepath.Join(dir, "subuid")
	hostenv.SubGIDPath = filepath.Join(dir, "subgid")
	if err := os.WriteFile(hostenv.SubUIDPath, []byte("bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := Spec{Command: "true", Dir: dir, User: &userns.User{MapUID: os.Geteuid(), MapGID: os.Getegid()}}
	var pe *PermanentError
	if err := CheckSpawn(s, false); !errors.As(err, &pe) {
		t.Fatalf("want PermanentError for bad subuid, got %v", err)
	}
}
