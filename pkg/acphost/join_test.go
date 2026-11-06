package acphost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func needNsenter(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("nsenter"); err != nil {
		t.Skip("nsenter not on PATH")
	}
}

func pin(t *testing.T, types ...string) *Pinned {
	t.Helper()
	needNsenter(t)
	needUserns(t)
	p, err := Pin(types...)
	if err != nil {
		t.Skipf("pin: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func joinSpec(t *testing.T, dir, script string, join map[string]string) Spec {
	agent := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\n"+script), 0o755))
	return Spec{Protocol: Protocol, Session: "s1", Command: agent, Dir: dir, GCRootDir: dir, LeaseMS: 60_000, Join: join}
}

func TestJoinNetAndUser(t *testing.T) {
	p := pin(t, "net")
	dir := t.TempDir()
	s := joinSpec(t, dir, `readlink /proc/self/ns/net >"$PWD/net"; readlink /proc/self/ns/user >"$PWD/user"; id -u >"$PWD/uid"`+"\n", p.Paths)
	require.NoError(t, CheckSpawn(s, false))
	require.NoError(t, WriteSpec(dir, s))
	require.NoError(t, Main(dir))
	ex, _ := ReadExit(dir)
	require.Equal(t, 0, ex.ExitCode, "%+v", ex)
	for _, typ := range []string{"net", "user"} {
		want, err := os.Readlink(p.Paths[typ])
		require.NoError(t, err)
		got, err := os.ReadFile(filepath.Join(dir, typ))
		require.NoError(t, err)
		assert.Equal(t, want, strings.TrimSpace(string(got)), typ)
	}
	uid, err := os.ReadFile(filepath.Join(dir, "uid"))
	require.NoError(t, err)
	assert.Equal(t, "0\n", string(uid), "Pin maps our uid to 0")

	iso, ok, err := ReadIsolation(dir)
	require.NoError(t, err)
	require.True(t, ok)
	want, _ := os.Readlink(p.Paths["net"])
	assert.Equal(t, JoinApplied{Path: p.Paths["net"], NS: want}, iso.Join["net"])
}

func TestJoinPid(t *testing.T) {
	p := pin(t, "pid")
	dir := t.TempDir()
	s := joinSpec(t, dir, `readlink /proc/self/ns/pid >"$PWD/pid"`+"\n", p.Paths)
	require.NoError(t, CheckSpawn(s, false))
	require.NoError(t, WriteSpec(dir, s))
	require.NoError(t, Main(dir))
	ex, _ := ReadExit(dir)
	require.Equal(t, 0, ex.ExitCode, "%+v", ex)
	want, err := os.Readlink(p.Paths["pid"])
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(dir, "pid"))
	require.NoError(t, err)
	assert.Equal(t, want, strings.TrimSpace(string(got)))
}

func TestCheckJoinRejects(t *testing.T) {
	needNsenter(t)
	dir := t.TempDir()
	u := testUser()
	for name, tc := range map[string]struct {
		s    func(Spec) Spec
		want string
	}{
		"unknown type": {func(s Spec) Spec { s.Join = map[string]string{"time": "/proc/self/ns/time"}; return s }, "unknown namespace type"},
		"wrong type":   {func(s Spec) Spec { s.Join = map[string]string{"net": "/proc/self/ns/ipc"}; return s }, "is a ipc namespace"},
		"not a ns":     {func(s Spec) Spec { s.Join = map[string]string{"net": dir}; return s }, "not a namespace"},
		"with user": {func(s Spec) Spec {
			s.Join, s.User = map[string]string{"net": "/proc/self/ns/net"}, &u
			return s
		}, "cannot combine join with user"},
		"net with pasta": {func(s Spec) Spec {
			s.Join, s.Sandbox = map[string]string{"net": "/proc/self/ns/net"}, &Sandbox{Network: "pasta"}
			return s
		}, "conflicts with sandbox network"},
		"net with none": {func(s Spec) Spec {
			s.Join, s.Sandbox = map[string]string{"net": "/proc/self/ns/net"}, &Sandbox{Network: "none"}
			return s
		}, "conflicts with sandbox network"},
		"own user ns": {func(s Spec) Spec { s.Join = map[string]string{"user": "/proc/self/ns/user"}; return s }, "needs CAP_SYS_ADMIN"},
	} {
		t.Run(name, func(t *testing.T) {
			if os.Geteuid() == 0 && name == "own user ns" {
				t.Skip("root")
			}
			err := CheckSpawn(tc.s(joinSpec(t, t.TempDir(), "true\n", nil)), false)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.True(t, Permanent(err))
		})
	}
}

func TestJoinPlanOrder(t *testing.T) {
	s := Spec{Join: map[string]string{"mnt": "m", "net": "n", "user": "u"}}
	assert.Equal(t, []string{"user=u", "net=n", "mnt=m"}, JoinPlan(s))
}
