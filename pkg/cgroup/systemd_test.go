package cgroup

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidSlice(t *testing.T) {
	for _, s := range []string{"acphost.slice", "acphost-sessions.slice", "a_b:c.d.slice"} {
		assert.True(t, ValidSlice(s), s)
	}
	for _, s := range []string{"", ".slice", "acphost", "acphost.scope", "/sys/fs/cgroup/x", "a--b.slice", "-a.slice", "a-.slice", "a b.slice", "a/b.slice"} {
		assert.False(t, ValidSlice(s), s)
	}
}

func TestScopeUnit(t *testing.T) {
	u, err := ScopeUnit("js-s-0033k", 42)
	require.NoError(t, err)
	// A dash inside the session is escaped, so it is not read as a
	// slice level.
	assert.Equal(t, `acphost-js\x2ds\x2d0033k-42.scope`, u)
	u, err = ScopeUnit("", 7)
	require.NoError(t, err)
	assert.Equal(t, "acphost-7.scope", u)
	u, err = ScopeUnit(".x y", 1)
	require.NoError(t, err)
	assert.Equal(t, `acphost-\x2ex\x20y-1.scope`, u)
	_, err = ScopeUnit(strings.Repeat("a", 250), 1)
	assert.True(t, isPermanent(err), "%v", err)

	u, err = HostScopeUnit("js-s-0033k", 42)
	require.NoError(t, err)
	assert.Equal(t, `acphost-host-js\x2ds\x2d0033k-42.scope`, u)
}

// A host scope has no limits and is not delegated: the host runs in it
// directly (#240).
func TestHostScopeProps(t *testing.T) {
	got := map[string]any{}
	for _, p := range hostScopeProps("acphost-hosts.slice", "s1", 99) {
		got[p.Name] = p.Value.Value()
	}
	assert.Equal(t, []uint32{99}, got["PIDs"])
	assert.Equal(t, "acphost-hosts.slice", got["Slice"])
	assert.Equal(t, "continue", got["OOMPolicy"])
	assert.Equal(t, "inactive-or-failed", got["CollectMode"])
	for _, k := range []string{"Delegate", "MemoryMax", "TasksMax", "CPUQuotaPerSecUSec"} {
		assert.NotContains(t, got, k)
	}
	for _, p := range hostScopeProps("", "s1", 99) {
		assert.NotEqual(t, "Slice", p.Name)
	}
	_, err := StartHostScope(context.Background(), "/sys/fs/cgroup/x", "s1", 1)
	assert.True(t, isPermanent(err), "%v", err)
}

func TestScopeProps(t *testing.T) {
	c := Cgroup{Driver: DriverSystemd, Parent: "acphost.slice", CPUMax: "50000 100000", MemoryMax: "1073741824", MemoryHigh: "max", SwapMax: "0", PidsMax: 512}
	props, err := scopeProps(c, "s1", 99)
	require.NoError(t, err)
	got := map[string]any{}
	for _, p := range props {
		got[p.Name] = p.Value.Value()
	}
	assert.Equal(t, []uint32{99}, got["PIDs"])
	assert.Equal(t, "acphost.slice", got["Slice"])
	assert.Equal(t, true, got["Delegate"])
	assert.Equal(t, "continue", got["OOMPolicy"])
	assert.Equal(t, uint64(1073741824), got["MemoryMax"])
	assert.Equal(t, systemdInfinity, got["MemoryHigh"])
	assert.Equal(t, uint64(0), got["MemorySwapMax"])
	assert.Equal(t, uint64(512), got["TasksMax"])
	// 50000/100000 is half a CPU: 500ms of CPU per second.
	assert.Equal(t, uint64(500_000), got["CPUQuotaPerSecUSec"])
	assert.Equal(t, uint64(100_000), got["CPUQuotaPeriodUSec"])

	// Unset limits and a "max" quota add no property; no slice keeps
	// the manager's default.
	props, err = scopeProps(Cgroup{Driver: DriverSystemd, CPUMax: "max 100000"}, "s1", 1)
	require.NoError(t, err)
	for _, p := range props {
		assert.NotContains(t, []string{"Slice", "MemoryMax", "MemoryHigh", "MemorySwapMax", "TasksMax", "CPUQuotaPerSecUSec"}, p.Name)
	}
}

func TestCheckSystemd(t *testing.T) {
	require.NoError(t, Check(Cgroup{Driver: DriverSystemd}))
	require.NoError(t, Check(Cgroup{Driver: DriverSystemd, Parent: "acphost.slice", MemoryMax: "1024"}))
	for _, c := range []Cgroup{
		{Driver: DriverSystemd, Parent: "/sys/fs/cgroup/x"},
		{Driver: DriverSystemd, MemoryMax: "1G"},
		{Driver: "docker"},
	} {
		err := Check(c)
		assert.True(t, isPermanent(err), "%+v: %v", c, err)
	}
}

func TestScopeErrorPermanent(t *testing.T) {
	err := scopeError("u.scope", dbus.Error{Name: "org.freedesktop.DBus.Error.InvalidArgs", Body: []any{"bad"}})
	assert.True(t, isPermanent(err), "%v", err)
	err = scopeError("u.scope", dbus.Error{Name: "org.freedesktop.systemd1.UnitExists"})
	assert.False(t, isPermanent(err), "%v", err)
}

func TestWaitJob(t *testing.T) {
	sigs := make(chan *Signal, 4)
	job := dbus.ObjectPath("/org/freedesktop/systemd1/job/7")
	sigs <- &Signal{Name: systemdManager + ".JobRemoved", Body: []any{uint32(6), dbus.ObjectPath("/org/freedesktop/systemd1/job/6"), "other.scope", "failed"}}
	sigs <- &Signal{Name: systemdManager + ".JobRemoved", Body: []any{uint32(7), job, "u.scope", "done"}}
	require.NoError(t, waitJob(context.Background(), sigs, job, "u.scope"))

	sigs <- &Signal{Name: systemdManager + ".JobRemoved", Body: []any{uint32(7), job, "u.scope", "failed"}}
	assert.ErrorContains(t, waitJob(context.Background(), sigs, job, "u.scope"), "start job failed")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	assert.ErrorIs(t, waitJob(ctx, sigs, job, "u.scope"), context.DeadlineExceeded)
}

// Signal is dbus.Signal, short for the test above.
type Signal = dbus.Signal

func TestManagerSocket(t *testing.T) {
	if os.Geteuid() == 0 {
		assert.Equal(t, "/run/systemd/private", ManagerSocket())
		return
	}
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/77")
	assert.Equal(t, "/run/user/77/systemd/private", ManagerSocket())
}
