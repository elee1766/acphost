package acphost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoFieldsTouchesNothing runs the host lifecycle with Cgroup, User and
// Sandbox unset and checks that no isolation helper is looked up or run
// and no cgroupfs or subordinate id file is read (#37).
func TestNoFieldsTouchesNothing(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	f := func(s string) { mu.Lock(); seen = append(seen, s); mu.Unlock() }
	traceHook.Store(&f)
	t.Cleanup(func() { traceHook.Store(nil) })
	// No subuid file and no cgroup must not matter either.
	subuidPath, subgidPath = "/nonexistent/subuid", "/nonexistent/subgid"
	t.Cleanup(func() { subuidPath, subgidPath = "/etc/subuid", "/etc/subgid" })

	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\necho '{\"jsonrpc\":\"2.0\",\"method\":\"session/update\"}'\nexec cat >/dev/null\n"), 0o700))
	s := Spec{Protocol: Protocol, Session: "s1", Command: agent, Dir: dir, GCRootDir: dir, LeaseMS: 60_000}
	require.NoError(t, CheckSpawn(s, false))
	require.NoError(t, WriteSpec(dir, s))
	done := make(chan error, 1)
	go func() { done <- Main(dir) }()
	reconnectTwice(t, dir)
	stopHost(t, dir, done)
	ex, _ := ReadExit(dir)
	assert.Equal(t, "controller requested stop", ex.Reason)

	mu.Lock()
	defer mu.Unlock()
	for _, s := range seen {
		assert.False(t, strings.HasPrefix(s, "cgroupfs:") || strings.HasPrefix(s, "subid:") || strings.HasPrefix(s, "run:"), s)
		for _, h := range []string{"newuidmap", "newgidmap", "pasta", "nft", "bwrap"} {
			assert.NotEqual(t, "look:"+h, s)
		}
	}
	assert.Contains(t, seen, "look:"+agent, "trace hook is wired")
}

func reconnectTwice(t *testing.T, dir string) {
	t.Helper()
	for range 2 {
		var c interface{ Close() error }
		require.Eventually(t, func() bool {
			nc, _, _, err := Connect(context.Background(), dir)
			if err == nil {
				c = nc
			}
			return err == nil
		}, 5*time.Second, 10*time.Millisecond)
		require.NoError(t, c.Close())
	}
}

func stopHost(t *testing.T, dir string, done <-chan error) {
	t.Helper()
	c, _, _, err := Connect(context.Background(), dir)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	_, err = c.Write([]byte(`{"jsonrpc":"2.0","method":"` + MethodStop + `"}` + "\n"))
	require.NoError(t, err)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("host did not stop")
	}
}

// killWith SIGKILLs every process whose command line has token, so a
// failed test does not leak sleeps.
func killWith(token string) {
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err == nil && strings.Contains(string(b), token) {
			var pid int
			if _, err := fmt.Sscan(e.Name(), &pid); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
}

type features struct{ cgroup, user, pasta bool }

func (f features) String() string {
	var p []string
	if f.cgroup {
		p = append(p, "cgroup")
	}
	if f.user {
		p = append(p, "user")
	}
	if f.pasta {
		p = append(p, "pasta")
	}
	return strings.Join(p, "+")
}

var matrix = []features{{cgroup: true}, {user: true}, {pasta: true}, {cgroup: true, user: true, pasta: true}}

// matrixSpec returns a spec with f's features whose agent is script, or
// skips when the environment cannot provide one of them.
func matrixSpec(t *testing.T, f features, dir, script string) Spec {
	t.Helper()
	var s Spec
	if f.pasta {
		bwrap := needBwrap(t)
		pasta, _ := needPasta(t)
		s = sandboxSpec(t, bwrap, dir, script)
		s.Sandbox.Network = "pasta"
		s.Sandbox.Pasta = pasta
	} else {
		agent := filepath.Join(dir, "agent")
		require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\n"+script), 0o755))
		s = Spec{Protocol: Protocol, Session: "s1", Command: agent, Dir: dir, GCRootDir: dir, LeaseMS: 60_000}
	}
	if f.user {
		needUserns(t)
		u := testUser()
		s.User = &u
	}
	if f.cgroup {
		parent := needCgroup(t)
		s.Cgroup = &Cgroup{Parent: parent, PidsMax: 128}
		// The session name is the cgroup name, so keep it unique per
		// test in a shared parent.
		s.Session = fmt.Sprintf("s%d", time.Now().UnixNano())
		t.Cleanup(func() {
			d := filepath.Join(parent, s.Session)
			cgroupKill(d)
			waitCgroupEmpty(d, 5*time.Second)
			_ = os.Remove(d)
		})
	}
	require.NoError(t, CheckSpawn(s, false))
	return s
}

// grandchildScript starts a background grandchild carrying token, then
// waits so the agent shell (whose command line has its own path) stays
// alive. With a cgroup the grandchild calls setsid, leaving the process
// group, so only cgroup.kill can reach it.
func grandchildScript(f features, token string) string {
	setsid := ""
	if f.cgroup {
		setsid = "setsid "
	}
	return setsid + "sh -c 'sleep 600; :' " + token + " </dev/null >/dev/null 2>&1 &\n" +
		`echo '{"jsonrpc":"2.0","method":"session/update"}'` + "\nsleep 600 &\nwait\n"
}

func TestMatrixExitCode(t *testing.T) {
	for _, f := range matrix {
		t.Run(f.String(), func(t *testing.T) {
			dir := t.TempDir()
			s := matrixSpec(t, f, dir, "exit 3\n")
			require.NoError(t, WriteSpec(dir, s))
			require.NoError(t, Main(dir))
			ex, _ := ReadExit(dir)
			assert.Equal(t, f.cgroup, ex.Cgroup != nil, "cgroup stats only with a cgroup")
			ex.Cgroup = nil
			assert.Equal(t, Exit{Reason: "agent exited", ExitCode: 3}, ex)
			checkIsolation(t, f, dir, s)
		})
	}
}

// checkIsolation checks isolation.json against what matrixSpec set up.
func checkIsolation(t *testing.T, f features, dir string, s Spec) {
	t.Helper()
	iso, ok, err := ReadIsolation(dir)
	require.NoError(t, err)
	require.True(t, ok)
	if f.cgroup {
		assert.Equal(t, &CgroupApplied{Dir: filepath.Join(s.Cgroup.Parent, s.Session), Limits: []LimitApplied{{"pids.max", "128"}}}, iso.Cgroup)
	} else {
		assert.Nil(t, iso.Cgroup)
	}
	if f.user {
		assert.Equal(t, &UserApplied{
			UIDMap: []string{fmt.Sprintf("%d %d 1", s.User.UID, s.User.MapUID)},
			GIDMap: []string{fmt.Sprintf("%d %d 1", s.User.GID, s.User.MapGID)},
		}, iso.User)
	} else {
		assert.Nil(t, iso.User)
	}
	if f.pasta {
		assert.Equal(t, &NetworkApplied{Mode: "pasta", PastaStarted: true}, iso.Network)
		require.NotEmpty(t, iso.Bwrap)
		assert.Equal(t, s.Sandbox.Pasta, iso.Bwrap[0])
		assert.Contains(t, iso.Bwrap, s.Sandbox.Bwrap)
		assert.Equal(t, s.Command, iso.Bwrap[len(iso.Bwrap)-1])
		assert.Equal(t, []string{"/proc/self/exe"}, iso.PdeathHelper, "pdeath helper recorded before pasta")
	} else {
		assert.Nil(t, iso.Network)
		assert.Nil(t, iso.Bwrap)
		assert.Nil(t, iso.PdeathHelper)
	}
}

func TestMatrixStopKillsGrandchild(t *testing.T) {
	for _, f := range matrix {
		t.Run(f.String(), func(t *testing.T) {
			// Flavours share nothing (own dir, token, cgroup name), and
			// the pasta ones spend ~5s stopping.
			t.Parallel()
			dir := t.TempDir()
			token := "acphost-mx-" + filepath.Base(dir)
			t.Cleanup(func() { killWith(token) })
			s := matrixSpec(t, f, dir, grandchildScript(f, token))
			require.NoError(t, WriteSpec(dir, s))
			done := make(chan error, 1)
			go func() { done <- Main(dir) }()
			require.Eventually(t, func() bool { return procWith(token) }, 5*time.Second, 20*time.Millisecond)
			reconnectTwice(t, dir)
			stopHost(t, dir, done)
			assert.Eventually(t, func() bool { return !procWith(token) }, 5*time.Second, 50*time.Millisecond, "grandchild killed")
			assert.False(t, procWith(s.Command), "agent killed")
			ex, _ := ReadExit(dir)
			assert.Equal(t, "controller requested stop", ex.Reason)
		})
	}
}

func TestMatrixLeaseKillsGrandchild(t *testing.T) {
	for _, f := range matrix {
		t.Run(f.String(), func(t *testing.T) {
			t.Parallel() // see TestMatrixStopKillsGrandchild
			dir := t.TempDir()
			token := "acphost-mx-" + filepath.Base(dir)
			t.Cleanup(func() { killWith(token) })
			s := matrixSpec(t, f, dir, grandchildScript(f, token))
			s.LeaseMS = 500
			require.NoError(t, WriteSpec(dir, s))
			done := make(chan error, 1)
			go func() { done <- Main(dir) }()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(20 * time.Second):
				t.Fatal("host did not exit on lease")
			}
			assert.Eventually(t, func() bool { return !procWith(token) }, 5*time.Second, 50*time.Millisecond, "grandchild killed")
			ex, _ := ReadExit(dir)
			assert.Equal(t, "no controller for 500ms", ex.Reason)
		})
	}
}

// TestMatrixHostHelper is the host process for TestMatrixPdeathsig: it
// runs Main when started with ACPHOST_TEST_HOST_DIR and is a no-op
// otherwise.
func TestMatrixHostHelper(t *testing.T) {
	dir := os.Getenv("ACPHOST_TEST_HOST_DIR")
	if dir == "" {
		t.Skip("helper for TestMatrixPdeathsig")
	}
	_ = Main(dir)
}

func TestMatrixPdeathsig(t *testing.T) {
	for _, f := range matrix {
		t.Run(f.String(), func(t *testing.T) {
			dir := t.TempDir()
			token := "acphost-mx-" + filepath.Base(dir)
			t.Cleanup(func() { killWith(token) })
			s := matrixSpec(t, f, dir, grandchildScript(f, token))
			require.NoError(t, WriteSpec(dir, s))
			host := exec.Command(os.Args[0], "-test.run=^TestMatrixHostHelper$")
			host.Env = append(os.Environ(), "ACPHOST_TEST_HOST_DIR="+dir)
			require.NoError(t, host.Start())
			require.Eventually(t, func() bool { return procWith(token) && procWith(s.Command) }, 10*time.Second, 20*time.Millisecond)
			require.NoError(t, host.Process.Kill())
			_ = host.Wait()
			if !assert.Eventually(t, func() bool { return !procWith(s.Command) }, 5*time.Second, 50*time.Millisecond, "agent died with the host") {
				t.Log(procTree(s.Command))
			}
		})
	}
}
