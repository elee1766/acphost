package proc_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/elee1766/acphost/cgroup"
	"github.com/elee1766/acphost/proc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestStartExitCodeAndOutput(t *testing.T) {
	var out bytes.Buffer
	p, err := proc.Start(context.Background(), proc.Spec{Command: "sh", Args: []string{"-c", "echo hi; exit 3"}, Dir: t.TempDir()}, proc.Stdout(&out))
	require.NoError(t, err)
	ex, err := p.Wait()
	require.NoError(t, err)
	assert.Equal(t, proc.Exit{ExitCode: 3}, ex)
	assert.Equal(t, "hi\n", out.String())
	assert.Equal(t, proc.Isolation{}, p.Isolation())
}

// alive reports whether pid exists and is not a zombie.
func alive(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	f := strings.Fields(string(b[bytes.LastIndexByte(b, ')')+1:]))
	return len(f) > 0 && f[0] != "Z"
}

// waitGone waits until pid is gone; Wait already killed the group, so
// this only covers reaping by init.
func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still alive", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// readPid reads a pid the command wrote to a fifo, so the test knows the
// child forked without sleeping.
func readPid(t *testing.T, fifo string) int {
	t.Helper()
	b, err := os.ReadFile(fifo)
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	require.NoError(t, err)
	return pid
}

func mkfifo(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "pid")
	require.NoError(t, unix.Mkfifo(p, 0o600))
	return p
}

// A ctx deadline kills the command and a child that left its process
// group, through the cgroup.
func TestStartCtxDeadlineKillsCgroup(t *testing.T) {
	parent := needCgroup(t)
	fifo := mkfifo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// setsid moves the grandchild out of the process group, so only the
	// cgroup reaches it.
	script := fmt.Sprintf(`setsid sh -c 'echo $$ >%s; exec sleep 600' & wait`, fifo)
	p, err := proc.Start(ctx, proc.Spec{
		Command: "sh", Args: []string{"-c", script}, Dir: t.TempDir(),
		Cgroup: &cgroup.Cgroup{Parent: parent, PidsMax: 64}, Name: "ctx",
	})
	require.NoError(t, err)
	gc := readPid(t, fifo)
	require.True(t, alive(gc))
	dir := p.CgroupDir()
	cancel()
	ex, err := p.Wait()
	require.NoError(t, err)
	assert.Equal(t, "killed", ex.Signal)
	assert.Empty(t, ex.SetupError)
	require.NotNil(t, ex.Cgroup)
	waitGone(t, gc)
	_, err = os.Stat(dir)
	assert.True(t, os.IsNotExist(err), "cgroup removed")
}

// Without a cgroup, a ctx deadline kills the process group.
func TestStartCtxDeadlineKillsGroup(t *testing.T) {
	fifo := mkfifo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	script := fmt.Sprintf(`sh -c 'echo $$ >%s; exec sleep 600' & wait`, fifo)
	p, err := proc.Start(ctx, proc.Spec{Command: "sh", Args: []string{"-c", script}, Dir: t.TempDir()})
	require.NoError(t, err)
	gc := readPid(t, fifo)
	cancel()
	ex, err := p.Wait()
	require.NoError(t, err)
	assert.Equal(t, "killed", ex.Signal)
	waitGone(t, gc)
}

func TestStopTermThenWait(t *testing.T) {
	p, err := proc.Start(context.Background(), proc.Spec{Command: "sleep", Args: []string{"600"}, Dir: t.TempDir()})
	require.NoError(t, err)
	p.Stop(time.Minute)
	ex, err := p.Wait()
	require.NoError(t, err)
	assert.Equal(t, "terminated", ex.Signal)
}

// The command dies with the caller: a helper process (this test binary)
// starts sleep with proc.Start and is SIGKILLed.
func TestStartKilledWhenCallerDies(t *testing.T) {
	fifo := mkfifo(t)
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Env = append(os.Environ(), "PROC_TEST_START_CHILD="+fifo)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	pid := readPid(t, fifo)
	require.True(t, alive(pid))
	require.NoError(t, cmd.Process.Signal(syscall.SIGKILL))
	_ = cmd.Wait()
	waitGone(t, pid)
}

// startChild is the caller in TestStartKilledWhenCallerDies: it starts
// sleep, writes its pid to fifo and blocks.
func startChild(fifo string) {
	// Pdeathsig follows the thread that started the child.
	runtime.LockOSThread()
	p, err := proc.Start(context.Background(), proc.Spec{Command: "sleep", Args: []string{"600"}, Dir: "/"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(fifo, []byte(strconv.Itoa(p.Pid())), 0); err != nil {
		os.Exit(1)
	}
	_, _ = p.Wait() // until the test kills this process
	os.Exit(1)
}

// needCgroup skips unless this process has a writable delegated cgroup
// with pids, and returns a fresh parent removed at the end of the test.
func needCgroup(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("ACPHOST_TEST_CGROUP"); p != "" {
		return p
	}
	info, err := cgroup.Delegated()
	if err != nil || !info.Writable {
		t.Skipf("no writable delegated cgroup: %v", err)
	}
	sub, _ := os.ReadFile(filepath.Join(info.Path, "cgroup.subtree_control"))
	if !strings.Contains(string(sub), "pids") {
		t.Skipf("%s/cgroup.subtree_control lacks pids", info.Path)
	}
	parent := filepath.Join(info.Path, fmt.Sprintf("acphost-proc-test-%d", time.Now().UnixNano()))
	require.NoError(t, os.Mkdir(parent, 0o755))
	t.Cleanup(func() { _ = os.Remove(parent) })
	if err := os.WriteFile(filepath.Join(parent, "cgroup.subtree_control"), []byte("+pids"), 0); err != nil {
		t.Skipf("enable pids: %v", err)
	}
	return parent
}
