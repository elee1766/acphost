package cgroup

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/elee1766/acphost/pkg/trace"
	"github.com/elee1766/acphost/pkg/xerr"
	"github.com/godbus/dbus/v5"
)

// The systemd driver (#226). With cgroupfs and Delegate=, the caller's
// unit must enable controllers for session cgroups that outlive it, and
// systemd then cannot start the next process of that unit into it
// (clone3 CLONE_INTO_CGROUP EBUSY). With this driver the session cgroup
// is a transient scope the manager owns, in a slice of its own, so the
// caller's unit enables nothing.

const (
	systemdDest    = "org.freedesktop.systemd1"
	systemdPath    = dbus.ObjectPath("/org/freedesktop/systemd1")
	systemdManager = "org.freedesktop.systemd1.Manager"
	// systemdInfinity is how systemd spells "max" for a byte or task
	// limit property.
	systemdInfinity = uint64(math.MaxUint64)
	// maxUnitName is systemd's UNIT_NAME_MAX.
	maxUnitName = 255
)

// Scope is a transient systemd scope made by StartScope.
type Scope struct {
	// Unit is the scope's unit name.
	Unit string
	// Dir is its cgroup directory under /sys/fs/cgroup.
	Dir string
}

// unitProp is one (name, value) unit property, D-Bus signature (sv).
type unitProp struct {
	Name  string
	Value dbus.Variant
}

// ValidSlice reports whether s is a slice unit name systemd accepts:
// "<name>.slice" with unit name characters only, and no empty dash
// component.
func ValidSlice(s string) bool {
	name, ok := strings.CutSuffix(s, ".slice")
	if !ok || name == "" || len(s) > maxUnitName {
		return false
	}
	for _, part := range strings.Split(name, "-") {
		if part == "" {
			return false
		}
	}
	for _, r := range name {
		if !unitNameChar(r) {
			return false
		}
	}
	return true
}

func unitNameChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(":_.-", r)
}

// ScopeUnit is the unit name StartScope uses for a session and the pid
// it adopts: acphost-<session>-<pid>.scope, the session escaped as
// systemd-escape does. The pid keeps a restarted session from meeting
// its previous scope while systemd still collects it.
func ScopeUnit(session string, pid int) (string, error) {
	var b strings.Builder
	b.WriteString("acphost-")
	if session != "" {
		for i := range len(session) {
			c := session[i]
			switch {
			case c == '/':
				b.WriteByte('-')
			case c < 0x80 && unitNameChar(rune(c)) && c != '-' && (c != '.' || i > 0):
				b.WriteByte(c)
			default:
				fmt.Fprintf(&b, `\x%02x`, c)
			}
		}
		b.WriteByte('-')
	}
	b.WriteString(strconv.Itoa(pid))
	b.WriteString(".scope")
	if b.Len() > maxUnitName {
		return "", xerr.Permanentf("cgroup: scope unit name for session %q is %d bytes, over the limit of %d", session, b.Len(), maxUnitName)
	}
	return b.String(), nil
}

// scopeProps are the unit properties StartScope sets: c's limits, the
// accounting the stats need, and how the scope behaves.
func scopeProps(c Cgroup, session string, pid int) ([]unitProp, error) {
	p := []unitProp{
		{"Description", dbus.MakeVariant("acphost session " + session)},
		{"PIDs", dbus.MakeVariant([]uint32{uint32(pid)})},
		// The caller makes a leaf for the command under the scope.
		{"Delegate", dbus.MakeVariant(true)},
		// Stats read memory.*, cpu.stat and pids from the scope's dir.
		{"MemoryAccounting", dbus.MakeVariant(true)},
		{"CPUAccounting", dbus.MakeVariant(true)},
		{"TasksAccounting", dbus.MakeVariant(true)},
		// As with cgroupfs: an OOM kill ends one process, and the
		// command decides what happens next. The default, stop, would
		// kill the whole scope.
		{"OOMPolicy", dbus.MakeVariant("continue")},
		// Gone once empty, also when a process in it failed.
		{"CollectMode", dbus.MakeVariant("inactive-or-failed")},
	}
	if c.Parent != "" {
		p = append(p, unitProp{"Slice", dbus.MakeVariant(c.Parent)})
	}
	for _, x := range []struct{ prop, value string }{
		{"MemoryMax", c.MemoryMax}, {"MemoryHigh", c.MemoryHigh}, {"MemorySwapMax", c.SwapMax},
	} {
		if x.value == "" {
			continue
		}
		n, err := systemdBytes(x.value)
		if err != nil {
			return nil, err
		}
		p = append(p, unitProp{x.prop, dbus.MakeVariant(n)})
	}
	if c.PidsMax > 0 {
		p = append(p, unitProp{"TasksMax", dbus.MakeVariant(uint64(c.PidsMax))})
	}
	if c.CPUMax != "" {
		f := strings.Fields(c.CPUMax)
		if len(f) != 2 {
			return nil, xerr.Permanentf("cgroup cpu_max %q: want \"<quota|max> <period>\"", c.CPUMax)
		}
		period, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil || period == 0 {
			return nil, xerr.Permanentf("cgroup cpu_max %q: bad period", c.CPUMax)
		}
		// "max" is systemd's default, no quota.
		if f[0] != "max" {
			quota, err := strconv.ParseUint(f[0], 10, 64)
			if err != nil || quota == 0 {
				return nil, xerr.Permanentf("cgroup cpu_max %q: bad quota", c.CPUMax)
			}
			p = append(p,
				unitProp{"CPUQuotaPerSecUSec", dbus.MakeVariant(quota * 1_000_000 / period)},
				unitProp{"CPUQuotaPeriodUSec", dbus.MakeVariant(period)})
		}
	}
	return p, nil
}

func systemdBytes(s string) (uint64, error) {
	if s == "max" {
		return systemdInfinity, nil
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, xerr.Permanentf("cgroup: %q: want a byte count or max", s)
	}
	return n, nil
}

// ManagerSocket is the private socket of the systemd manager this
// process uses: the system manager as root, else the user's own
// manager.
func ManagerSocket() string {
	if os.Geteuid() == 0 {
		return "/run/systemd/private"
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = fmt.Sprintf("/run/user/%d", os.Geteuid())
	}
	return filepath.Join(dir, "systemd", "private")
}

// dialManager connects to ManagerSocket. The private socket needs no
// bus daemon, and the manager sends signals to it directly.
func dialManager() (*dbus.Conn, error) {
	sock := ManagerSocket()
	conn, err := dbus.Dial("unix:path=" + dbus.EscapeBusAddressValue(sock))
	if err != nil {
		return nil, xerr.Permanentf("cgroup: systemd manager at %s: %w", sock, err)
	}
	if err := conn.Auth([]dbus.Auth{dbus.AuthExternal(strconv.Itoa(os.Geteuid()))}); err != nil {
		_ = conn.Close()
		return nil, xerr.Permanentf("cgroup: systemd manager at %s: auth: %w", sock, err)
	}
	return conn, nil
}

// CheckManager connects to the systemd manager and asks for its
// version, without changing anything. Preflight uses it.
func CheckManager() error {
	trace.Event("systemd:check-manager")
	conn, err := dialManager()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_, err = conn.Object(systemdDest, systemdPath).GetProperty(systemdManager + ".Version")
	if err != nil {
		return xerr.Permanentf("cgroup: systemd manager at %s: %w", ManagerSocket(), err)
	}
	return nil
}

// StartScope asks the systemd manager (see ManagerSocket) for a
// transient scope in the slice c.Parent that adopts pid, with c's limits
// as unit properties, and waits for the start job. The scope is
// delegated: the caller may make child cgroups in Dir. It returns the
// scope and the limit files as the kernel has them once it started.
//
// pid must be a process of the caller's uid, and a non-root caller must
// run in its manager's cgroup tree: moving pid needs write access to
// cgroup.procs of the common ancestor. The scope goes away once it is
// empty; there is no stop call.
func StartScope(ctx context.Context, c Cgroup, session string, pid int) (Scope, []LimitApplied, error) {
	trace.Event("systemd:start-scope")
	unit, err := ScopeUnit(session, pid)
	if err != nil {
		return Scope{}, nil, err
	}
	props, err := scopeProps(c, session, pid)
	if err != nil {
		return Scope{}, nil, err
	}
	conn, err := dialManager()
	if err != nil {
		return Scope{}, nil, err
	}
	defer func() { _ = conn.Close() }()
	// Registered before the start call, so a job that ends before the
	// call returns is still seen.
	sigs := make(chan *dbus.Signal, 16)
	conn.Signal(sigs)
	mgr := conn.Object(systemdDest, systemdPath)
	if err := mgr.CallWithContext(ctx, systemdManager+".Subscribe", 0).Err; err != nil {
		return Scope{}, nil, fmt.Errorf("cgroup: systemd subscribe: %w", err)
	}
	var job dbus.ObjectPath
	aux := []struct {
		Name  string
		Props []unitProp
	}{}
	if err := mgr.CallWithContext(ctx, systemdManager+".StartTransientUnit", 0, unit, "fail", props, aux).Store(&job); err != nil {
		return Scope{}, nil, scopeError(unit, err)
	}
	if err := waitJob(ctx, sigs, job, unit); err != nil {
		return Scope{}, nil, err
	}
	var path dbus.ObjectPath
	if err := mgr.CallWithContext(ctx, systemdManager+".GetUnit", 0, unit).Store(&path); err != nil {
		return Scope{}, nil, fmt.Errorf("cgroup: systemd scope %s: %w", unit, err)
	}
	var cg string
	if err := conn.Object(systemdDest, path).StoreProperty("org.freedesktop.systemd1.Scope.ControlGroup", &cg); err != nil {
		return Scope{}, nil, fmt.Errorf("cgroup: systemd scope %s: control group: %w", unit, err)
	}
	if cg == "" {
		return Scope{}, nil, fmt.Errorf("cgroup: systemd scope %s: no control group", unit)
	}
	s := Scope{Unit: unit, Dir: filepath.Join("/sys/fs/cgroup", cg)}
	// Report what the kernel has, not what was asked: systemd rounds the
	// CPU quota, and a missing controller leaves no file.
	var applied []LimitApplied
	for _, x := range c.limits() {
		b, err := os.ReadFile(filepath.Join(s.Dir, x.file))
		if err != nil {
			return s, applied, xerr.Permanentf("cgroup: systemd scope %s: %s not applied (is the %s controller delegated to the manager?): %w", unit, x.file, x.controller, err)
		}
		applied = append(applied, LimitApplied{File: x.file, Value: strings.TrimSpace(string(b))})
	}
	return s, applied, nil
}

// waitJob waits for the JobRemoved signal of job.
func waitJob(ctx context.Context, sigs <-chan *dbus.Signal, job dbus.ObjectPath, unit string) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("cgroup: systemd scope %s: waiting for start job: %w", unit, ctx.Err())
		case s, ok := <-sigs:
			if !ok {
				return fmt.Errorf("cgroup: systemd scope %s: manager connection closed", unit)
			}
			// JobRemoved(u id, o job, s unit, s result)
			if s.Name != systemdManager+".JobRemoved" || len(s.Body) < 4 {
				continue
			}
			if p, _ := s.Body[1].(dbus.ObjectPath); p != job {
				continue
			}
			if r, _ := s.Body[3].(string); r != "done" {
				return fmt.Errorf("cgroup: systemd scope %s: start job %s (a non-root caller must run in its manager's cgroup tree to move a process into a scope)", unit, r)
			}
			return nil
		}
	}
}

// scopeError marks errors a retry cannot fix as permanent.
func scopeError(unit string, err error) error {
	var de dbus.Error
	if errors.As(err, &de) {
		switch de.Name {
		case "org.freedesktop.DBus.Error.InvalidArgs", "org.freedesktop.DBus.Error.AccessDenied",
			"org.freedesktop.DBus.Error.PropertyReadOnly", "org.freedesktop.systemd1.NoSuchUnit":
			return xerr.Permanentf("cgroup: systemd scope %s: %s: %w", unit, de.Name, err)
		}
	}
	return fmt.Errorf("cgroup: systemd scope %s: %w", unit, err)
}
