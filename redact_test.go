package acphost

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const secretNote = `{"jsonrpc":"2.0","method":"hi","params":{"token":"s3cret"}}`

// startRedactHost runs Main with an agent that prints secretNote and then
// lingers, and returns a channel with Main's result.
func startRedactHost(t *testing.T, dir string, linger string, opts ...Option) chan error {
	t.Helper()
	agent := filepath.Join(dir, "agent")
	script := "#!/bin/sh\necho '" + secretNote + "'\nsleep " + linger + "\n"
	require.NoError(t, os.WriteFile(agent, []byte(script), 0o700))
	require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol, Session: "s1", Command: agent, Dir: dir, GCRootDir: dir}))
	done := make(chan error, 1)
	go func() { done <- Main(dir, opts...) }()
	return done
}

func waitHost(t *testing.T, done chan error) {
	t.Helper()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("host did not exit")
	}
}

func journalMsgs(t *testing.T, dir string) []string {
	t.Helper()
	frames, _, err := ReadJournal(filepath.Join(dir, JournalFile), 0)
	require.NoError(t, err)
	var out []string
	for _, f := range frames {
		out = append(out, string(f.Msg))
	}
	return out
}

func waitJournal(t *testing.T, dir string) {
	t.Helper()
	require.Eventually(t, func() bool {
		seq, _ := JournalTail(dir)
		return seq >= 1
	}, 5*time.Second, 10*time.Millisecond)
}

// dialSince connects with hello.Since and returns the first agent line
// after the welcome.
func dialSince(t *testing.T, dir string, since int64) string {
	t.Helper()
	var nc net.Conn
	require.Eventually(t, func() bool {
		var err error
		nc, err = net.Dial("unix", filepath.Join(dir, ACPSocket))
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	defer func() { _ = nc.Close() }()
	hello, _ := json.Marshal(Hello{Protocol: Protocol, Since: &since})
	_, err := nc.Write(append(hello, '\n'))
	require.NoError(t, err)
	rd := bufio.NewReader(nc)
	_ = nc.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = rd.ReadBytes('\n') // welcome
	require.NoError(t, err)
	line, err := rd.ReadBytes('\n')
	require.NoError(t, err)
	return string(bytes.TrimSpace(line))
}

func TestRedactorJournalAndReplay(t *testing.T) {
	dir := t.TempDir()
	var froms []string
	done := startRedactHost(t, dir, "2", WithRedactor(func(from string, msg []byte) []byte {
		froms = append(froms, from)
		return bytes.ReplaceAll(msg, []byte("s3cret"), []byte("REDACTED"))
	}))
	waitJournal(t, dir)
	redacted := `{"jsonrpc":"2.0","method":"hi","params":{"token":"REDACTED"}}`

	// Reconnects asking for everything after seq 0 replay from the journal.
	assert.Equal(t, redacted, dialSince(t, dir, 0))
	assert.Equal(t, redacted, dialSince(t, dir, 0))
	waitHost(t, done)

	assert.Equal(t, []string{redacted}, journalMsgs(t, dir))
	raw, err := os.ReadFile(filepath.Join(dir, JournalFile))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "s3cret")
	assert.Equal(t, []string{"agent"}, froms)
}

func TestNoRedactorJournalUnchanged(t *testing.T) {
	dir := t.TempDir()
	done := startRedactHost(t, dir, "0")
	waitHost(t, done)
	raw, err := os.ReadFile(filepath.Join(dir, JournalFile))
	require.NoError(t, err)
	frames, _, err := ReadJournal(filepath.Join(dir, JournalFile), 0)
	require.NoError(t, err)
	require.Len(t, frames, 1)
	f := frames[0]
	want := `{"seq":1,"t":` + jsonInt(f.T) + `,"from":"agent","msg":` + secretNote + "}\n"
	assert.Equal(t, want, string(raw))
}

func TestRedactorInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	done := startRedactHost(t, dir, "0", WithRedactor(func(string, []byte) []byte {
		return []byte(`{"broken`)
	}))
	waitHost(t, done)
	assert.Equal(t, []string{"null"}, journalMsgs(t, dir))
}

func jsonInt(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
