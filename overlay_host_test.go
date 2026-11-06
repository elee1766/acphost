package acphost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elee1766/acphost/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An overlay works with a mapped User: bwrap mounts it before the agent
// runs as the inside uid (#163).
func TestUsernsOverlay(t *testing.T) {
	needUserns(t)
	dir := t.TempDir()
	s := sandboxSpec(t, needBwrap(t), dir, "cat \"$PWD/merged/f\" >\"$PWD/out\"\n")
	var layers []string
	for i, v := range []string{"one", "two", "three"} {
		l := filepath.Join(dir, "layer", string(rune('a'+i)))
		require.NoError(t, os.MkdirAll(l, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(l, "f"), []byte(v), 0o644))
		layers = append(layers, l)
	}
	s.Sandbox.Overlay = []sandbox.Overlay{{Dst: filepath.Join(s.Dir, "merged"), Layers: layers}}
	u := testUser()
	s.User = &u
	require.NoError(t, CheckSpawn(s, false))
	require.NoError(t, WriteSpec(dir, s))
	require.NoError(t, Main(dir))
	ex, ok := ReadExit(dir)
	require.True(t, ok)
	assert.Equal(t, Exit{Reason: "agent exited"}, ex)
	b, err := os.ReadFile(filepath.Join(s.Dir, "out"))
	require.NoError(t, err)
	assert.Equal(t, "three", string(b))
}
