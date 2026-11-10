package acphost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/gfx-labs/acphost/pkg/userns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// needUserns skips unless newuidmap and newgidmap are on PATH and the
// kernel lets this process create a user namespace.
func needUserns(t *testing.T) {
	t.Helper()
	for _, c := range []string{"newuidmap", "newgidmap"} {
		if _, err := exec.LookPath(c); err != nil {
			t.Skipf("%s not on PATH", c)
		}
	}
	c := exec.Command("/bin/sh", "-c", "true")
	c.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWUSER}
	if err := c.Run(); err != nil {
		t.Skipf("user namespaces do not work here: %v", err)
	}
}

// testUser maps inside 1000:1000 to the caller's own uid and gid, which
// newuidmap allows without a subuid entry, so the test needs no host setup
// beyond the helpers. Test dirs are private to the caller, so mapping to
// a subordinate id would leave the agent unable to reach them.
func testUser() userns.User {
	return userns.User{UID: 1000, GID: 1000, MapUID: os.Geteuid(), MapGID: os.Getegid()}
}

func usernsSpecs(t *testing.T, script string) map[string]func(dir string) Spec {
	return map[string]func(string) Spec{
		"direct": func(dir string) Spec {
			agent := filepath.Join(dir, "agent")
			require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\n"+script), 0o755))
			u := testUser()
			return Spec{Protocol: Protocol, Session: "s1", Command: agent, Dir: dir, GCRootDir: dir, LeaseMS: 60_000, User: &u}
		},
		"sandbox": func(dir string) Spec {
			bwrap := needBwrap(t)
			s := sandboxSpec(t, bwrap, dir, script)
			u := testUser()
			s.User = &u
			return s
		},
	}
}

func TestUsernsFileOwnerAndExit(t *testing.T) {
	needUserns(t)
	for name, mk := range usernsSpecs(t, "id -u >\"$PWD/inside\"; touch \"$PWD/made\"; exit 3\n") {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s := mk(dir)
			require.NoError(t, CheckSpawn(s, false))
			require.NoError(t, WriteSpec(dir, s))
			require.NoError(t, Main(dir))
			ex, ok := ReadExit(dir)
			require.True(t, ok)
			assert.Equal(t, Exit{Reason: "agent exited", ExitCode: 3}, ex)
			b, err := os.ReadFile(filepath.Join(s.Dir, "inside"))
			require.NoError(t, err)
			assert.Equal(t, "1000\n", string(b), "uid inside the namespace")
			st, err := os.Stat(filepath.Join(s.Dir, "made"))
			require.NoError(t, err)
			sys := st.Sys().(*syscall.Stat_t)
			assert.Equal(t, uint32(os.Geteuid()), sys.Uid, "mapped host uid")
			assert.Equal(t, uint32(os.Getegid()), sys.Gid, "mapped host gid")
			// The session dir and socket stay the host user's.
			hs, err := os.Stat(filepath.Join(dir, SpecFile))
			require.NoError(t, err)
			assert.Equal(t, uint32(os.Geteuid()), hs.Sys().(*syscall.Stat_t).Uid)
		})
	}
}

func TestUsernsReconnect(t *testing.T) {
	needUserns(t)
	for name, mk := range usernsSpecs(t, `echo '{"jsonrpc":"2.0","method":"session/update"}'`+"\nexec cat >/dev/null\n") {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, WriteSpec(dir, mk(dir)))
			done := make(chan error, 1)
			go func() { done <- Main(dir) }()
			ctx := context.Background()
			for range 2 {
				var nc interface{ Close() error }
				require.Eventually(t, func() bool {
					c, _, _, err := Connect(ctx, dir)
					if err == nil {
						nc = c
					}
					return err == nil
				}, 5*time.Second, 10*time.Millisecond)
				require.Eventually(t, func() bool { seq, _ := JournalTail(dir); return seq > 0 }, 5*time.Second, 10*time.Millisecond)
				_ = nc.Close()
			}
			st, err := os.Stat(filepath.Join(dir, ACPSocket))
			require.NoError(t, err)
			assert.Equal(t, uint32(os.Geteuid()), st.Sys().(*syscall.Stat_t).Uid, "socket owned by host user")
			c, _, _, err := Connect(ctx, dir)
			require.NoError(t, err)
			_, err = c.Write([]byte(`{"jsonrpc":"2.0","method":"` + MethodStop + `"}` + "\n"))
			require.NoError(t, err)
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(15 * time.Second):
				t.Fatal("host did not stop")
			}
			_ = c.Close()
		})
	}
}

func TestUsernsMapFailureIsPermanent(t *testing.T) {
	needUserns(t)
	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	// A failing newuidmap stands in for a refused map: its stderr must
	// reach the error.
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "newuidmap"), []byte("#!/bin/sh\necho nope >&2\nexit 1\n"), 0o755))
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	u := testUser()
	require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol, Session: "s1", Command: agent, Dir: dir, GCRootDir: dir, LeaseMS: 60_000, User: &u}))
	err := Main(dir)
	require.Error(t, err)
	assert.True(t, Permanent(err))
	assert.Contains(t, err.Error(), "nope")
}
