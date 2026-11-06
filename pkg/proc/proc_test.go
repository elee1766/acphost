package proc_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/elee1766/acphost/pkg/proc"
	"github.com/elee1766/acphost/pkg/reexec"
	"github.com/elee1766/acphost/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	proc.MaybeChild()
	if dir := os.Getenv("PROC_TEST_DETACH_CHILD"); dir != "" {
		detachChild(dir)
	}
	if fifo := os.Getenv("PROC_TEST_START_CHILD"); fifo != "" {
		startChild(fifo)
	}
	if os.Getenv("PROC_TEST_PLAIN") != "" {
		// Reached main: no helper ran.
		_, _ = os.Stdout.WriteString("plain main\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func self(t *testing.T, argv0 string, extra ...string) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(exe, extra...)
	cmd.Args[0] = argv0
	return cmd
}

// The old env triggers do nothing now: the process runs its own main.
func TestStrayEnvDoesNothing(t *testing.T) {
	cmd := self(t, "proc.test", "true")
	cmd.Env = append(os.Environ(), "PROC_TEST_PLAIN=1",
		"_ACPHOST_USERNS_CHILD=0:0:", "_ACPHOST_PDEATH_CHILD=1", "_ACPHOST_DNS_CHECK=127.0.0.1 example.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Equal(t, "plain main\n", string(out))
}

// A helper argv[0] with no config fd fails closed.
func TestHelperWithoutConfigExits127(t *testing.T) {
	for _, name := range []string{reexec.Userns, reexec.Pdeath} {
		cmd := self(t, name, "true")
		cmd.Env = append(os.Environ(), "PROC_TEST_PLAIN=1")
		out, err := cmd.CombinedOutput()
		var ee *exec.ExitError
		require.ErrorAs(t, err, &ee, name)
		assert.Equal(t, 127, ee.ExitCode(), name)
		assert.NotContains(t, string(out), "plain main", name)
	}
}

// Malformed config, or a parent pid that is not the parent, exits 127.
func TestHelperBadConfigExits127(t *testing.T) {
	for _, tc := range []struct {
		name string
		fd   int
		body string
	}{
		{reexec.Userns, reexec.UsernsFd, "not json"},
		{reexec.Userns, reexec.UsernsFd, `{"uid":0,"gid":0,"parent":1}`},
		{reexec.Pdeath, reexec.PdeathFd, "{"},
		{reexec.Pdeath, reexec.PdeathFd, `{"parent":1}`},
	} {
		r, w, err := os.Pipe()
		require.NoError(t, err)
		_, err = w.WriteString(tc.body)
		require.NoError(t, err)
		require.NoError(t, w.Close())
		cmd := self(t, tc.name, "true")
		cmd.ExtraFiles = reexec.SetExtraFile(nil, tc.fd, r)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		out, err := cmd.CombinedOutput()
		_ = r.Close()
		var ee *exec.ExitError
		require.ErrorAs(t, err, &ee, "%s %s: %s", tc.name, tc.body, out)
		assert.Equal(t, 127, ee.ExitCode(), "%s %s", tc.name, tc.body)
	}
}

// Without MaybeChild, a spawn that needs a helper fails with the named
// error before starting anything.
func TestNoMaybeChildNamedError(t *testing.T) {
	old := reexec.SetCalled(false)
	t.Cleanup(func() { reexec.SetCalled(old) })

	_, _, _, _, err := reexec.PdeathWrap([]string{"true"})
	assert.ErrorIs(t, err, proc.ErrNoMaybeChild)

	err = sandbox.CheckDNS(sandbox.Sandbox{Network: "pasta", Pasta: "/bin/true", ResolvConf: writeConf(t)}, "example.com")
	assert.ErrorIs(t, err, proc.ErrNoMaybeChild)
}

func writeConf(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "resolv.conf")
	require.NoError(t, os.WriteFile(p, []byte("nameserver 127.0.0.1\n"), 0o600))
	return p
}

// Importing acphost has no side effects: no package has an init().
func TestNoInit(t *testing.T) {
	root := "../.."
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "init" {
				return errors.New(p + " has func init")
			}
		}
		return nil
	})
	require.NoError(t, err)
}
