package acphost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// Cgroup places the agent in a cgroup v2 child of Parent and sets limits
// on it. Empty strings and a zero PidsMax leave that limit unset.
type Cgroup struct {
	// Parent is an absolute path to a delegated cgroup directory, such as
	// one found with DelegatedCgroup. The session cgroup is made under it.
	Parent string `json:"parent"`
	// CPUMax is written to cpu.max: "<quota|max> <period>" in microseconds.
	CPUMax string `json:"cpu_max,omitempty"`
	// MemoryMax and MemoryHigh are a byte count or "max".
	MemoryMax  string `json:"memory_max,omitempty"`
	MemoryHigh string `json:"memory_high,omitempty"`
	// SwapMax is written to memory.swap.max: a byte count or "max". Unset
	// keeps the kernel default (no swap limit), so on a host with swap an
	// agent at MemoryMax swaps instead of being OOM killed. Set "0" to make
	// MemoryMax a hard cap.
	SwapMax string `json:"swap_max,omitempty"`
	// PidsMax is written to pids.max when positive.
	PidsMax int64 `json:"pids_max,omitempty"`
}

// cgroupLimit is one limit file and the controller it needs.
type cgroupLimit struct{ controller, file, value string }

func (c Cgroup) limits() []cgroupLimit {
	var l []cgroupLimit
	if c.CPUMax != "" {
		l = append(l, cgroupLimit{"cpu", "cpu.max", c.CPUMax})
	}
	if c.MemoryMax != "" {
		l = append(l, cgroupLimit{"memory", "memory.max", c.MemoryMax})
	}
	if c.MemoryHigh != "" {
		l = append(l, cgroupLimit{"memory", "memory.high", c.MemoryHigh})
	}
	if c.SwapMax != "" {
		l = append(l, cgroupLimit{"memory", "memory.swap.max", c.SwapMax})
	}
	if c.PidsMax > 0 {
		l = append(l, cgroupLimit{"pids", "pids.max", strconv.FormatInt(c.PidsMax, 10)})
	}
	return l
}

// checkCgroup validates c against the cgroup tree. Errors are permanent:
// a retry cannot fix a bad value or a missing controller.
func checkCgroup(c Cgroup) error {
	trace("cgroupfs:check")
	if !filepath.IsAbs(c.Parent) {
		return Permanentf("cgroup parent %q is not absolute", c.Parent)
	}
	if st, err := os.Stat(c.Parent); err != nil {
		return Permanentf("cgroup parent: %w", err)
	} else if !st.IsDir() {
		return Permanentf("cgroup parent %s is not a directory", c.Parent)
	}
	if c.CPUMax != "" && !validCPUMax(c.CPUMax) {
		return Permanentf("cgroup cpu_max %q: want \"<quota|max> <period>\"", c.CPUMax)
	}
	if c.MemoryMax != "" && !validBytes(c.MemoryMax) {
		return Permanentf("cgroup memory_max %q: want a byte count or max", c.MemoryMax)
	}
	if c.MemoryHigh != "" && !validBytes(c.MemoryHigh) {
		return Permanentf("cgroup memory_high %q: want a byte count or max", c.MemoryHigh)
	}
	if c.SwapMax != "" && !validBytes(c.SwapMax) {
		return Permanentf("cgroup swap_max %q: want a byte count or max", c.SwapMax)
	}
	if c.PidsMax < 0 {
		return Permanentf("cgroup pids_max %d is negative", c.PidsMax)
	}
	l := c.limits()
	if len(l) == 0 {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(c.Parent, "cgroup.subtree_control"))
	if err != nil {
		return Permanentf("cgroup parent: %w", err)
	}
	enabled := strings.Fields(string(b))
	for _, x := range l {
		if !slices.Contains(enabled, x.controller) {
			return Permanentf("cgroup %s: controller %q not in %s/cgroup.subtree_control", x.file, x.controller, c.Parent)
		}
	}
	return nil
}

func validCPUMax(s string) bool {
	f := strings.Fields(s)
	if len(f) != 2 {
		return false
	}
	if f[0] != "max" {
		if q, err := strconv.ParseUint(f[0], 10, 64); err != nil || q == 0 {
			return false
		}
	}
	p, err := strconv.ParseUint(f[1], 10, 64)
	return err == nil && p > 0
}

func validBytes(s string) bool {
	if s == "max" {
		return true
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

// prepareCgroup creates <parent>/<session> and writes the set limits. It
// returns the new cgroup directory. The session name must be a single
// path element so nothing is written outside Parent.
func prepareCgroup(c Cgroup, session string) (string, error) {
	trace("cgroupfs:prepare")
	if session == "" || session == "." || session == ".." || strings.ContainsAny(session, "/\x00") {
		return "", Permanentf("cgroup: bad session name %q", session)
	}
	dir := filepath.Join(c.Parent, session)
	if filepath.Dir(dir) != filepath.Clean(c.Parent) {
		return "", Permanentf("cgroup: %s escapes %s", dir, c.Parent)
	}
	if err := os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("cgroup: %w", err)
	}
	for _, x := range c.limits() {
		if err := os.WriteFile(filepath.Join(dir, x.file), []byte(x.value), 0o644); err != nil {
			return dir, fmt.Errorf("cgroup %s: %w", x.file, err)
		}
	}
	return dir, nil
}

// removeCgroup removes a cgroup made by prepareCgroup. cgroupfs refuses
// rmdir while processes remain, so this only succeeds once they exit.
func removeCgroup(dir string) error {
	trace("cgroupfs:remove")
	err := os.Remove(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// DelegatedInfo describes the cgroup this process runs in.
type DelegatedInfo struct {
	// Path is the cgroup directory under the cgroupfs mount.
	Path string
	// Writable reports whether this process may create children in Path.
	Writable bool
	// Controllers lists what Path may enable for its children (from
	// cgroup.controllers).
	Controllers []string
}

// DelegatedCgroup returns this process's cgroup v2 directory. A caller
// usually makes a child of it to use as Cgroup.Parent, since a cgroup
// that holds processes cannot enable controllers for its children.
func DelegatedCgroup() (DelegatedInfo, error) {
	return delegatedCgroup("/proc/self/cgroup", "/sys/fs/cgroup")
}

// delegatedCgroup is DelegatedCgroup with the proc file and cgroupfs root
// injectable for tests.
func delegatedCgroup(procFile, root string) (DelegatedInfo, error) {
	trace("cgroupfs:delegated")
	b, err := os.ReadFile(procFile)
	if err != nil {
		return DelegatedInfo{}, fmt.Errorf("cgroup: %w", err)
	}
	var rel string
	found := false
	for line := range strings.SplitSeq(string(b), "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok {
			rel, found = p, true
			break
		}
	}
	if !found {
		return DelegatedInfo{}, fmt.Errorf("cgroup: no v2 entry in %s", procFile)
	}
	info := DelegatedInfo{Path: filepath.Join(root, filepath.Clean("/"+rel))}
	if c, err := os.ReadFile(filepath.Join(info.Path, "cgroup.controllers")); err == nil {
		info.Controllers = strings.Fields(string(c))
	}
	info.Writable = syscall.Access(info.Path, 2 /* W_OK */) == nil
	return info, nil
}
