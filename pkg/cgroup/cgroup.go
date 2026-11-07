package cgroup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/elee1766/acphost/pkg/trace"
	"github.com/elee1766/acphost/pkg/xerr"
)

// Drivers for Cgroup.Driver.
const (
	// DriverCgroupfs makes the session cgroup with mkdir under a
	// delegated Parent directory and writes the limit files. The default.
	DriverCgroupfs = "cgroupfs"
	// DriverSystemd asks the caller's systemd manager for a transient
	// scope in the Parent slice with the limits as unit properties
	// (see StartScope), the split podman and the kubelet use.
	DriverSystemd = "systemd"
)

// Cgroup places the agent in a cgroup v2 child of Parent and sets limits
// on it. Empty strings and a zero PidsMax leave that limit unset.
type Cgroup struct {
	// Driver is DriverCgroupfs (also when empty) or DriverSystemd.
	Driver string `json:"driver,omitempty"`
	// Parent is, with cgroupfs, an absolute path to a delegated cgroup
	// directory, such as one found with Delegated; the session cgroup is
	// made under it. With systemd it is a slice name such as
	// "acphost.slice" (empty: the manager's default).
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

// Controllers lists the controllers c's limits need, in limit order,
// without repeats.
func (c Cgroup) Controllers() []string {
	var need []string
	for _, x := range c.limits() {
		if !slices.Contains(need, x.controller) {
			need = append(need, x.controller)
		}
	}
	return need
}

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

// Check validates c against the cgroup tree (with systemd, only the
// values and the slice name). Errors are permanent: a retry cannot fix
// a bad value or a missing controller.
func Check(c Cgroup) error {
	if err := checkValues(c); err != nil {
		return err
	}
	switch c.Driver {
	case "", DriverCgroupfs:
	case DriverSystemd:
		trace.Event("systemd:check")
		if c.Parent != "" && !ValidSlice(c.Parent) {
			return xerr.Permanentf("cgroup parent %q is not a slice name (want <name>.slice)", c.Parent)
		}
		return nil
	default:
		return xerr.Permanentf("cgroup driver %q: want %s or %s", c.Driver, DriverCgroupfs, DriverSystemd)
	}
	trace.Event("cgroupfs:check")
	if !filepath.IsAbs(c.Parent) {
		return xerr.Permanentf("cgroup parent %q is not absolute", c.Parent)
	}
	if st, err := os.Stat(c.Parent); err != nil {
		return xerr.Permanentf("cgroup parent: %w", err)
	} else if !st.IsDir() {
		return xerr.Permanentf("cgroup parent %s is not a directory", c.Parent)
	}
	l := c.limits()
	if len(l) == 0 {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(c.Parent, "cgroup.subtree_control"))
	if err != nil {
		return xerr.Permanentf("cgroup parent: %w", err)
	}
	enabled := strings.Fields(string(b))
	for _, x := range l {
		if !slices.Contains(enabled, x.controller) {
			return xerr.Permanentf("cgroup %s: controller %q not in %s/cgroup.subtree_control", x.file, x.controller, c.Parent)
		}
	}
	return nil
}

// checkValues checks the limit values, the same for both drivers.
func checkValues(c Cgroup) error {
	if c.CPUMax != "" && !validCPUMax(c.CPUMax) {
		return xerr.Permanentf("cgroup cpu_max %q: want \"<quota|max> <period>\"", c.CPUMax)
	}
	if c.MemoryMax != "" && !validBytes(c.MemoryMax) {
		return xerr.Permanentf("cgroup memory_max %q: want a byte count or max", c.MemoryMax)
	}
	if c.MemoryHigh != "" && !validBytes(c.MemoryHigh) {
		return xerr.Permanentf("cgroup memory_high %q: want a byte count or max", c.MemoryHigh)
	}
	if c.SwapMax != "" && !validBytes(c.SwapMax) {
		return xerr.Permanentf("cgroup swap_max %q: want a byte count or max", c.SwapMax)
	}
	if c.PidsMax < 0 {
		return xerr.Permanentf("cgroup pids_max %d is negative", c.PidsMax)
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

// Prepare creates <parent>/<session> and writes the set limits: the
// cgroupfs driver, whatever c.Driver says (proc.Start picks StartScope
// for DriverSystemd). It returns the new cgroup directory and the limits
// written, in order. The session name must be a single path element so
// nothing is written outside Parent.
func Prepare(c Cgroup, session string) (string, []LimitApplied, error) {
	trace.Event("cgroupfs:prepare")
	if session == "" || session == "." || session == ".." || strings.ContainsAny(session, "/\x00") {
		return "", nil, xerr.Permanentf("cgroup: bad session name %q", session)
	}
	dir := filepath.Join(c.Parent, session)
	if filepath.Dir(dir) != filepath.Clean(c.Parent) {
		return "", nil, xerr.Permanentf("cgroup: %s escapes %s", dir, c.Parent)
	}
	if err := os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return "", nil, fmt.Errorf("cgroup: %w", err)
	}
	var applied []LimitApplied
	for _, x := range c.limits() {
		if err := os.WriteFile(filepath.Join(dir, x.file), []byte(x.value), 0o644); err != nil {
			return dir, applied, fmt.Errorf("cgroup %s: %w", x.file, err)
		}
		applied = append(applied, LimitApplied{File: x.file, Value: x.value})
	}
	return dir, applied, nil
}

// Remove removes a cgroup made by Prepare. cgroupfs refuses
// rmdir while processes remain, so this only succeeds once they exit.
func Remove(dir string) error {
	trace.Event("cgroupfs:remove")
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

// Delegated returns this process's cgroup v2 directory. A caller
// usually makes a child of it to use as Cgroup.Parent, since a cgroup
// that holds processes cannot enable controllers for its children.
func Delegated() (DelegatedInfo, error) {
	return delegated("/proc/self/cgroup", "/sys/fs/cgroup")
}

// delegatedCgroup is Delegated with the proc file and cgroupfs root
// injectable for tests.
func delegated(procFile, root string) (DelegatedInfo, error) {
	trace.Event("cgroupfs:delegated")
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

// SetupDelegation prepares own, a delegated cgroup v2 directory such as
// DelegatedInfo.Path, to hold session cgroups. Cgroup v2 forbids
// processes in a cgroup whose children have controllers enabled, so it
// moves the calling process into <own>/controller, enables controllers
// in <own>/cgroup.subtree_control, creates <own>/sessions and enables
// them there too. It returns <own>/sessions for use as Cgroup.Parent.
//
// A repeat call, for example after a controller restart, succeeds and
// changes nothing. A controller missing from <own>/cgroup.controllers or
// a file that cannot be written returns a PermanentError naming the file.
func SetupDelegation(own string, controllers []string) (sessionsParent string, err error) {
	return setupDelegation(own, os.Getpid(), controllers)
}

// setupDelegation is SetupDelegation with the pid injectable so tests can
// run against a temp dir.
func setupDelegation(own string, pid int, controllers []string) (string, error) {
	trace.Event("cgroupfs:setup-delegation")
	avail, err := os.ReadFile(filepath.Join(own, "cgroup.controllers"))
	if err != nil {
		return "", xerr.Permanentf("cgroup: %w", err)
	}
	for _, c := range controllers {
		if !slices.Contains(strings.Fields(string(avail)), c) {
			return "", xerr.Permanentf("cgroup: controller %s not in %s", c, filepath.Join(own, "cgroup.controllers"))
		}
	}
	ctl := filepath.Join(own, "controller")
	if err := mkdirCgroup(ctl); err != nil {
		return "", err
	}
	procs := filepath.Join(ctl, "cgroup.procs")
	if !slices.Contains(ReadFields(procs), strconv.Itoa(pid)) {
		if err := writeCgroupFile(procs, strconv.Itoa(pid)); err != nil {
			return "", err
		}
	}
	if err := enableControllers(own, controllers); err != nil {
		return "", err
	}
	sessions := filepath.Join(own, "sessions")
	if err := mkdirCgroup(sessions); err != nil {
		return "", err
	}
	if err := enableControllers(sessions, controllers); err != nil {
		return "", err
	}
	return sessions, nil
}

// enableControllers writes only the controllers not yet in
// dir/cgroup.subtree_control, so a repeat call writes nothing.
func enableControllers(dir string, controllers []string) error {
	have := ReadFields(filepath.Join(dir, "cgroup.subtree_control"))
	var add []string
	for _, c := range controllers {
		if !slices.Contains(have, c) {
			add = append(add, "+"+c)
		}
	}
	if len(add) == 0 {
		return nil
	}
	return writeCgroupFile(filepath.Join(dir, "cgroup.subtree_control"), strings.Join(add, " "))
}

// ReadFields returns the whitespace-separated fields of file, nil if
// it cannot be read.
func ReadFields(file string) []string {
	b, _ := os.ReadFile(file)
	return strings.Fields(string(b))
}

func mkdirCgroup(dir string) error {
	if err := os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS) {
			return xerr.Permanentf("cgroup: %w", err)
		}
		return fmt.Errorf("cgroup: %w", err)
	}
	return nil
}

// writeCgroupFile writes value to file. Permission errors are permanent:
// retrying will not make the delegation writable.
func writeCgroupFile(file, value string) error {
	if err := os.WriteFile(file, []byte(value), 0o644); err != nil {
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS) {
			return xerr.Permanentf("cgroup %s: %w", file, err)
		}
		return fmt.Errorf("cgroup %s: %w", file, err)
	}
	return nil
}

// LimitApplied is one cgroup limit file and the value written to it.
type LimitApplied struct {
	File  string `json:"file"`
	Value string `json:"value"`
}
