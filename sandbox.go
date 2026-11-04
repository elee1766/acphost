package acphost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Sandbox describes how to confine the agent process. Only the
// "bubblewrap" backend exists.
type Sandbox struct {
	// Backend names the sandbox tool. Empty or "bubblewrap".
	Backend string `json:"backend,omitempty"`
	// Bwrap is the bwrap binary. Empty means look it up on PATH.
	Bwrap string `json:"bwrap,omitempty"`
	// RO lists read-only bind mounts.
	RO []Mount `json:"ro,omitempty"`
	// RW lists read-write bind mounts.
	RW []Mount `json:"rw,omitempty"`
	// Tmpfs lists empty tmpfs mounts. Only Dst is used.
	Tmpfs []Mount `json:"tmpfs,omitempty"`
	// Hide lists paths covered by an empty tmpfs after all binds.
	Hide []string `json:"hide,omitempty"`
	// EnvKeep names host env variables passed into the sandbox. All
	// others are cleared.
	EnvKeep []string `json:"env_keep,omitempty"`
	// Network is "host" (default) or "none".
	Network string `json:"network,omitempty"`
	// ExtraArgs are passed to bwrap after everything else, before "--".
	ExtraArgs []string `json:"extra_args,omitempty"`
}

// Mount is one mount in a Sandbox. In JSON it is either a path string
// (source and destination equal) or an object.
type Mount struct {
	Src string `json:"src"`
	// Dst defaults to Src.
	Dst string `json:"dst,omitempty"`
	// SkipMissing skips an RO mount whose source does not exist instead
	// of failing. It has no effect on RW mounts.
	SkipMissing bool `json:"skip_missing,omitempty"`
}

// UnmarshalJSON accepts a path string or a {"src","dst"} object.
func (m *Mount) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*m = Mount{Src: s}
		return nil
	}
	type plain Mount
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*m = Mount(p)
	return nil
}

func (m Mount) dst() string {
	if m.Dst != "" {
		return filepath.Clean(m.Dst)
	}
	return filepath.Clean(m.Src)
}

func pathDepth(p string) int {
	if p == "/" {
		return 0
	}
	return strings.Count(p, "/")
}

func within(p, root string) bool {
	if root == "/" {
		return strings.HasPrefix(p, "/")
	}
	return p == root || strings.HasPrefix(p, root+"/")
}

// SandboxArgs returns the full bwrap argv, starting with the bwrap path,
// that runs cmd with args in dir. It reads only the filesystem (to check
// sources) and the environment (for EnvKeep and PATH); the same inputs
// give the same output. dir must lie inside an RW mount's destination.
func SandboxArgs(s Sandbox, cmd string, args []string, dir string) ([]string, error) {
	if s.Backend != "" && s.Backend != "bubblewrap" {
		return nil, fmt.Errorf("sandbox: unknown backend %q", s.Backend)
	}
	var unshareNet bool
	switch s.Network {
	case "", "host":
	case "none":
		unshareNet = true
	default:
		return nil, fmt.Errorf("sandbox: unknown network %q", s.Network)
	}
	bwrap := s.Bwrap
	if bwrap == "" {
		p, err := exec.LookPath("bwrap")
		if err != nil {
			return nil, fmt.Errorf("sandbox: %w", err)
		}
		bwrap = p
	}

	type bind struct {
		flag, src, dst string
	}
	var binds []bind
	for _, m := range s.RO {
		if _, err := os.Stat(m.Src); err != nil {
			if errors.Is(err, os.ErrNotExist) && m.SkipMissing {
				continue
			}
			return nil, fmt.Errorf("sandbox: ro %s: %w", m.Src, err)
		}
		binds = append(binds, bind{"--ro-bind", m.Src, m.dst()})
	}
	for _, m := range s.RW {
		if _, err := os.Stat(m.Src); err != nil {
			return nil, fmt.Errorf("sandbox: rw %s: %w", m.Src, err)
		}
		binds = append(binds, bind{"--bind", m.Src, m.dst()})
	}
	for _, m := range s.Tmpfs {
		binds = append(binds, bind{"--tmpfs", "", m.dst()})
	}
	// Parents first. Stable, so at equal depth the input order holds and
	// a later mount at the same path wins.
	sort.SliceStable(binds, func(i, j int) bool {
		return pathDepth(binds[i].dst) < pathDepth(binds[j].dst)
	})

	cdir := filepath.Clean(dir)
	inRW := false
	for _, m := range s.RW {
		if within(cdir, m.dst()) {
			inRW = true
			break
		}
	}
	if !inRW {
		return nil, fmt.Errorf("sandbox: dir %s is not inside an rw mount", dir)
	}

	argv := []string{
		bwrap,
		"--die-with-parent", "--unshare-pid", "--new-session",
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp",
	}
	if unshareNet {
		argv = append(argv, "--unshare-net")
	}
	for _, b := range binds {
		if b.flag == "--tmpfs" {
			argv = append(argv, b.flag, b.dst)
		} else {
			argv = append(argv, b.flag, b.src, b.dst)
		}
	}
	for _, h := range s.Hide {
		argv = append(argv, "--tmpfs", filepath.Clean(h))
	}
	// The agent sees only the env it was given, so host secrets do not
	// leak in by default.
	argv = append(argv, "--clearenv")
	for _, k := range s.EnvKeep {
		if v, ok := os.LookupEnv(k); ok {
			argv = append(argv, "--setenv", k, v)
		}
	}
	argv = append(argv, "--chdir", cdir)
	argv = append(argv, s.ExtraArgs...)
	argv = append(argv, "--", cmd)
	return append(argv, args...), nil
}
