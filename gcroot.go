package acphost

import (
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// storePackage returns /nix/store/<hash>-name for a path inside the Nix
// store, or "" if the path is not in the store.
func storePackage(p string) string {
	const prefix = "/nix/store/"
	if !strings.HasPrefix(p, prefix) {
		return ""
	}
	name, _, _ := strings.Cut(p[len(prefix):], "/")
	if name == "" {
		return ""
	}
	return prefix + name
}

func gcRootDir(spec Spec) string {
	if spec.GCRootDir != "" {
		return spec.GCRootDir
	}
	name := os.Getenv("USER")
	if name == "" {
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
	}
	return filepath.Join("/nix/var/nix/gcroots/per-user", name)
}

// registerGCRoot links a GC root to the store package holding exe so
// nix-collect-garbage keeps the build while this host runs. It returns a
// cleanup func. Failures only log: outside NixOS the dir may not exist.
func registerGCRoot(log *slog.Logger, spec Spec, exe string) func() {
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	pkg := storePackage(exe)
	if pkg == "" {
		return func() {}
	}
	prefix := spec.GCRootPrefix
	if prefix == "" {
		prefix = DefaultGCRootPrefix
	}
	link := filepath.Join(gcRootDir(spec), prefix+spec.Session)
	_ = os.Remove(link) // stale link from a crashed host of this session
	if err := os.Symlink(pkg, link); err != nil {
		log.Warn("gc root", "err", err)
		return func() {}
	}
	return func() {
		if err := os.Remove(link); err != nil {
			log.Warn("remove gc root", "err", err)
		}
	}
}
