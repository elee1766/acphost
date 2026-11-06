package proc_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/elee1766/acphost/pkg/proc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetachedExitAndOutput(t *testing.T) {
	dir := t.TempDir()
	d, err := proc.StartDetached(dir, proc.Spec{Command: "sh", Args: []string{"-c", "echo hi; exit 4"}, Dir: dir}, time.Second)
	require.NoError(t, err)
	ex, err := d.Wait(context.Background())
	require.NoError(t, err)
	assert.Equal(t, proc.Exit{ExitCode: 4}, ex)
	b, err := os.ReadFile(filepath.Join(dir, proc.OutputFile))
	require.NoError(t, err)
	assert.Equal(t, "hi\n", string(b))
	_, err = d.Isolation()
	assert.NoError(t, err)
	assert.False(t, d.Running())
}

func TestDetachedStartError(t *testing.T) {
	dir := t.TempDir()
	_, err := proc.StartDetached(dir, proc.Spec{Command: "/nonexistent/x", Dir: dir}, time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/nonexistent/x")
}

func TestDetachedStop(t *testing.T) {
	dir := t.TempDir()
	d, err := proc.StartDetached(dir, proc.Spec{Command: "sleep", Args: []string{"600"}, Dir: dir}, time.Minute)
	require.NoError(t, err)
	require.True(t, d.Running())
	_, err = proc.StartDetached(dir, proc.Spec{Command: "true", Dir: dir}, time.Second)
	assert.ErrorContains(t, err, "already running")
	require.NoError(t, d.Stop())
	ex, err := d.Wait(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "terminated", ex.Signal)
}

// The command survives its caller dying, and Open from another process
// reports its exit.
func TestDetachedSurvivesCaller(t *testing.T) {
	dir := t.TempDir()
	release := filepath.Join(dir, "release")
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Env = append(os.Environ(), "PROC_TEST_DETACH_CHILD="+dir)
	out, err := cmd.Output()
	require.NoError(t, err, "caller")
	// The caller has exited; the command is still running.
	cpid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	require.NoError(t, err)
	require.True(t, alive(cpid), "command outlived its caller")
	d, err := proc.Open(dir)
	require.NoError(t, err)
	require.True(t, d.Running())
	_, ok := d.Exit()
	assert.False(t, ok)
	// Let the command finish: it waits for the fifo to open.
	f, err := os.OpenFile(release, os.O_WRONLY, 0)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	ex, err := d.Wait(context.Background())
	require.NoError(t, err)
	assert.Equal(t, proc.Exit{ExitCode: 7}, ex)
}

// detachChild is the caller in TestDetachedSurvivesCaller: it starts a
// command that blocks on a fifo, prints the command pid and exits.
func detachChild(dir string) {
	release := filepath.Join(dir, "release")
	if err := syscall.Mkfifo(release, 0o600); err != nil {
		os.Exit(1)
	}
	if _, err := proc.StartDetached(dir, proc.Spec{Command: "sh", Args: []string{"-c", "cat release >/dev/null; exit 7"}, Dir: dir}, time.Second); err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	// The status line carries the command pid only through Start; read
	// it from the process tree instead: the supervisor's child.
	d, err := proc.Open(dir)
	if err != nil {
		os.Exit(1)
	}
	b, _ := os.ReadFile("/proc/" + strconv.Itoa(d.Pid) + "/task/" + strconv.Itoa(d.Pid) + "/children")
	_, _ = os.Stdout.WriteString(strings.Fields(string(b) + " 0")[0] + "\n")
	os.Exit(0)
}
