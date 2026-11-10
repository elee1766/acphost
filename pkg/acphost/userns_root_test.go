package acphost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/gfx-labs/acphost/pkg/hostenv"
	"github.com/gfx-labs/acphost/pkg/userns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// subUser returns a User mapped to the first subordinate id block of the
// caller, or skips when it has none.
func subUser(t *testing.T) userns.User {
	t.Helper()
	self := os.Geteuid()
	subuid, err := userns.ReadSubIDs(hostenv.SubUIDPath, self)
	require.NoError(t, err)
	subgid, err := userns.ReadSubIDs(hostenv.SubGIDPath, self)
	require.NoError(t, err)
	a, err := userns.AllocSubIDs(t.TempDir(), subuid, subgid, 1)
	if err != nil {
		t.Skipf("no subid block for uid %d: %v", self, err)
	}
	t.Cleanup(func() { _ = a.Release() })
	u := a.User(1000, 1000)
	rep, err := PreflightIsolation(Isolated{User: &u})
	require.NoError(t, err)
	if !rep.OK() {
		t.Skipf("preflight: %+v", rep.Checks)
	}
	return u
}

// TestUsernsSandboxRootMode runs a sandboxed agent mapped to a
// subordinate uid whose mounts sit under a 0700 dir of the caller, which
// the subuid cannot traverse (#162). bwrap mounts as the caller, mapped
// to root inside, then the agent drops to User.UID with no caps.
func TestUsernsSandboxRootMode(t *testing.T) {
	needUserns(t)
	u := subUser(t)
	for _, pasta := range []bool{false, true} {
		name := "host"
		if pasta {
			name = "pasta"
		}
		t.Run(name, func(t *testing.T) {
			bwrap := needBwrap(t)
			dir := t.TempDir()
			require.NoError(t, os.Chmod(dir, 0o700))
			s := sandboxSpec(t, bwrap, dir, `id -u >"$PWD/uid"; id -g >>"$PWD/uid"; grep CapEff /proc/self/status >"$PWD/caps"; touch "$PWD/made"`+"\n")
			if pasta {
				p, _ := needPasta(t)
				s.Sandbox.Network, s.Sandbox.Pasta = "pasta", p
			}
			// The agent writes into work, so it must be writable to it.
			work := s.Dir
			require.NoError(t, os.Chmod(work, 0o777))
			require.NoError(t, os.Chmod(filepath.Join(work, "agent"), 0o755))
			s.User = &u
			require.NoError(t, CheckSpawn(s, false))
			require.NoError(t, WriteSpec(dir, s))
			require.NoError(t, Main(dir))
			ex, ok := ReadExit(dir)
			require.True(t, ok)
			require.Equal(t, Exit{Reason: "agent exited"}, ex)

			b, err := os.ReadFile(filepath.Join(work, "uid"))
			require.NoError(t, err)
			assert.Equal(t, "1000\n1000\n", string(b), "agent ids inside")
			b, err = os.ReadFile(filepath.Join(work, "caps"))
			require.NoError(t, err)
			assert.Equal(t, "CapEff:\t0000000000000000", strings.TrimSpace(string(b)))
			st, err := os.Stat(filepath.Join(work, "made"))
			require.NoError(t, err)
			sys := st.Sys().(*syscall.Stat_t)
			assert.Equal(t, uint32(u.MapUID), sys.Uid, "owned by MapUID on the host")
			assert.Equal(t, uint32(u.MapGID), sys.Gid, "owned by MapGID on the host")

			iso, ok, err := ReadIsolation(dir)
			require.NoError(t, err)
			require.True(t, ok)
			assert.Equal(t, &UserApplied{
				UIDMap: []string{fmt.Sprintf("0 %d 1", os.Geteuid()), fmt.Sprintf("1000 %d 1", u.MapUID)},
				GIDMap: []string{fmt.Sprintf("0 %d 1", os.Getegid()), fmt.Sprintf("1000 %d 1", u.MapGID)},
			}, iso.User)
		})
	}
}
