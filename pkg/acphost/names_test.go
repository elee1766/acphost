package acphost

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elee1766/acphost/pkg/cgroup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Session names in jobshop's js-s-<id> form and edge cases (#79).
func TestSessionNames(t *testing.T) {
	maxName := strings.Repeat("a", maxNameLen-len(DefaultGCRootPrefix))
	for name, tc := range map[string]struct {
		session string
		ok      bool
	}{
		"jobshop":      {"js-s-00338vc4km002", true},
		"extra dashes": {"js-s--00338-vc4-km002--", true},
		"leading dash": {"-js-s-1", true},
		"max length":   {maxName, true},
		"empty":        {"", true},
		"too long":     {maxName + "a", false},
		"slash":        {"js/s-1", false},
		"dotdot":       {"..", false},
		"nul":          {"js-s\x001", false},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "agent")
			require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o700))
			s := Spec{Session: tc.session, Dir: dir, Command: exe}
			err := CheckSpawn(s, false, WithHostDir(dir))
			if !tc.ok {
				assert.True(t, Permanent(err), "want PermanentError: %v", err)
				return
			}
			require.NoError(t, err)
			if tc.session == "" {
				return
			}

			// A session dir named after the session holds valid socket
			// and journal paths.
			sdir := filepath.Join(dir, tc.session)
			require.NoError(t, os.Mkdir(sdir, 0o700))
			jf, err := os.Create(filepath.Join(sdir, JournalFile))
			require.NoError(t, err)
			require.NoError(t, jf.Close())
			if CheckSocketPath(sdir) == nil {
				ln, err := net.Listen("unix", SocketPath(sdir))
				require.NoError(t, err)
				require.NoError(t, ln.Close())
			}

			// The cgroup dir is <parent>/<session>.
			parent := t.TempDir()
			cg, _, err := cgroup.Prepare(Cgroup{Parent: parent}, tc.session)
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(parent, tc.session), cg)
			st, err := os.Stat(cg)
			require.NoError(t, err)
			assert.True(t, st.IsDir())
		})
	}
}

// The socket check matches what the kernel accepts: a path of exactly
// MaxSocketPath bytes binds, one byte more is rejected early.
func TestSocketPathLimit(t *testing.T) {
	base := t.TempDir()
	pad := MaxSocketPath - len(SocketPath(base)) - 1 // "/" before the pad
	require.Positive(t, pad, "temp dir too long for this test")
	fits := filepath.Join(base, strings.Repeat("x", pad))
	over := fits + "y"
	require.NoError(t, os.Mkdir(fits, 0o700))
	require.NoError(t, os.Mkdir(over, 0o700))
	require.Len(t, SocketPath(fits), MaxSocketPath)

	require.NoError(t, CheckSocketPath(fits))
	ln, err := net.Listen("unix", SocketPath(fits))
	require.NoError(t, err)
	require.NoError(t, ln.Close())

	err = CheckSocketPath(over)
	assert.True(t, Permanent(err))
	assert.ErrorContains(t, err, "108")
	_, err = net.Listen("unix", SocketPath(over))
	assert.Error(t, err, "kernel rejects it too")

	exe := filepath.Join(base, "agent")
	require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o700))
	err = CheckSpawn(Spec{Session: "js-s-00338vc4km002", Dir: base, Command: exe}, false, WithHostDir(over))
	var pe *PermanentError
	assert.ErrorAs(t, err, &pe)
	assert.ErrorContains(t, err, "sun_path")
}
