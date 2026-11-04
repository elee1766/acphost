package acphost

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestStorePackage(t *testing.T) {
	cases := map[string]string{
		"/nix/store/abc-jobshop-1/bin/jobshop": "/nix/store/abc-jobshop-1",
		"/nix/store/abc-jobshop-1":             "/nix/store/abc-jobshop-1",
		"/usr/bin/jobshop":                     "",
		"/nix/store/":                          "",
	}
	for in, want := range cases {
		if got := storePackage(in); got != want {
			t.Errorf("storePackage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegisterGCRoot(t *testing.T) {
	dir := t.TempDir()
	spec := Spec{Session: "s1", GCRootDir: dir}
	log := slog.New(slog.DiscardHandler)
	link := filepath.Join(dir, "jobshop-host-s1")

	cleanup := registerGCRoot(log, spec, "/nix/store/abc-jobshop-1/bin/jobshop")
	if got, err := os.Readlink(link); err != nil || got != "/nix/store/abc-jobshop-1" {
		t.Fatalf("readlink = %q, %v", got, err)
	}
	cleanup()
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link not removed: %v", err)
	}

	registerGCRoot(log, spec, "/usr/bin/jobshop")()
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link created for non-store path: %v", err)
	}
}
