package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/elee1766/acphost/pkg/hostenv"
	"github.com/elee1766/acphost/pkg/userns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupError(t *testing.T) {
	assert.Empty(t, tapResult{last: "x"}.setupError(true, 0))
	assert.Equal(t, "x", tapResult{last: "x"}.setupError(true, 1))
	assert.Equal(t, "sandbox exited 1 before the agent ran", tapResult{}.setupError(true, 1))
	assert.Empty(t, tapResult{ranAgent: true, last: "x"}.setupError(true, 1))
	assert.Empty(t, tapResult{last: "x"}.setupError(false, 1), "unsandboxed agent output")
	assert.Equal(t, "acphost userns: e", tapResult{helperFail: "acphost userns: e", last: "y"}.setupError(false, 127))
}

func TestMapLines(t *testing.T) {
	assert.Equal(t, []string{"0 1000 1", "27 27 1"}, mapLines([]string{"0", "1000", "1", "27", "27", "1"}))
	assert.Nil(t, mapLines(nil))
}

func TestUsernsCommandApplied(t *testing.T) {
	dir := t.TempDir()
	oldU, oldG := hostenv.SubUIDPath, hostenv.SubGIDPath
	t.Cleanup(func() { hostenv.SubUIDPath, hostenv.SubGIDPath = oldU, oldG })
	hostenv.SubUIDPath, hostenv.SubGIDPath = filepath.Join(dir, "subuid"), filepath.Join(dir, "subgid")
	require.NoError(t, os.WriteFile(hostenv.SubUIDPath, nil, 0o600))
	require.NoError(t, os.WriteFile(hostenv.SubGIDPath, nil, 0o600))
	u := userns.User{UID: 0, GID: 0, MapUID: os.Geteuid(), MapGID: os.Getegid()}
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	_, applied, err := usernsCommand(cmd, u)
	require.NoError(t, err)
	assert.Equal(t, &UserApplied{
		UIDMap: []string{"0 " + strconv.Itoa(os.Geteuid()) + " 1"},
		GIDMap: []string{"0 " + strconv.Itoa(os.Getegid()) + " 1"},
	}, applied)
}
