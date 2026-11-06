package acphost_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elee1766/acphost/pkg/acphost"
)

func TestRenderer(t *testing.T) {
	frames, _, err := acphost.ReadJournal(filepath.Join("testdata", "journal.ndjson"), 0)
	require.NoError(t, err)
	var b strings.Builder
	r := &acphost.Renderer{W: &b}
	for _, f := range frames {
		r.Render(f)
	}
	want := `=== turn 06:58:26: You have claimed ticket js-1.
Reading the spec.
[plan]
  [x] read issue
  [>] write code
  [ ] open PR
[tool] Running shell command (pending)
[tool] Running shell command (completed)
Done.
=== turn end 06:58:27: end_turn
=== cancel requested
`
	assert.Equal(t, want, b.String())
}

func TestReadJournalSince(t *testing.T) {
	path := filepath.Join("testdata", "journal.ndjson")
	frames, _, err := acphost.ReadJournal(path, 10)
	require.NoError(t, err)
	require.Len(t, frames, 3)
	assert.Equal(t, int64(11), frames[0].Seq)

	frames, _, err = acphost.ReadJournal(path, -2)
	require.NoError(t, err)
	require.Len(t, frames, 2)
	assert.Equal(t, int64(12), frames[0].Seq)
}

func TestFollowJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.ndjson")
	require.NoError(t, os.WriteFile(path, []byte(`{"seq":1,"msg":{}}`+"\n"), 0o600))
	_, off, err := acphost.ReadJournal(path, 0)
	require.NoError(t, err)
	// A complete frame then a half-written one, both there before the
	// follower starts. Its first pass reports 2 and stops at the partial
	// line, so receiving 2 shows the partial was seen, with no sleep.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, _ = f.WriteString(`{"seq":2,"msg":{}}` + "\n" + `{"seq":3,`)

	ctx, cancel := context.WithCancel(t.Context())
	got := make(chan int64, 4)
	done := make(chan error, 1)
	go func() { done <- acphost.FollowJournal(ctx, path, off, func(f acphost.Frame) { got <- f.Seq }) }()
	next := func() int64 {
		select {
		case s := <-got:
			return s
		case <-time.After(5 * time.Second):
			t.Fatal("no frame followed")
			return 0
		}
	}
	assert.Equal(t, int64(2), next())
	// A half-written frame is reported once its newline arrives.
	_, _ = f.WriteString(`"msg":{}}` + "\n")
	_ = f.Close()
	assert.Equal(t, int64(3), next())
	cancel()
	assert.Empty(t, got, "each frame reported once")
	require.NoError(t, <-done)
}
