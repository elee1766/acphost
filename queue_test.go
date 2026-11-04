package acphost

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testHost is a Host with a journal and no agent process.
func testHost(t *testing.T) *Host {
	t.Helper()
	dir := t.TempDir()
	j, err := os.OpenFile(filepath.Join(dir, JournalFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = j.Close() })
	self, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	return &Host{
		dir: dir, journal: j, log: slog.New(slog.DiscardHandler),
		agent: &exec.Cmd{Process: self}, agentIn: nopWriteCloser{io.Discard},
		calls: map[string]pendingCall{}, agentReqs: map[string]json.RawMessage{},
		stopping: make(chan struct{}), disconnected: time.Now(),
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// agentOutput is n session/update notifications of about size bytes each,
// numbered from 1.
func agentOutput(n, size int) []byte {
	pad := bytes.Repeat([]byte("x"), size)
	var b bytes.Buffer
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `{"jsonrpc":"2.0","method":"session/update","params":{"n":%d,"pad":%q}}`+"\n", i, pad)
	}
	return b.Bytes()
}

// readNumbers reads n notifications from r and returns their numbers.
func readNumbers(t *testing.T, r io.Reader, n int) []int {
	t.Helper()
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, maxFrame)
	var got []int
	for len(got) < n && sc.Scan() {
		var m struct {
			Method string `json:"method"`
			Params struct {
				N int `json:"n"`
			} `json:"params"`
		}
		require.NoError(t, json.Unmarshal(sc.Bytes(), &m))
		if m.Method == "session/update" {
			got = append(got, m.Params.N)
		}
	}
	require.NoError(t, sc.Err())
	return got
}

func want(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i + 1
	}
	return out
}

const (
	floodFrames = 4000
	floodSize   = 8 << 10 // ~32 MB total, four times the queue bound
)

// TestBackpressureStalledController: a connected controller that never
// reads does not block the agent or grow the queue past its bound, and a
// late reader gets every frame in order, the overflow replayed from the
// journal.
func TestBackpressureStalledController(t *testing.T) {
	h := testHost(t)
	hostEnd, ctrlEnd := socketPair(t)
	h.gen++
	conn := newConn(h.gen, hostEnd, 0, 0)
	h.conn = conn
	go h.writeLoop(conn)

	done := make(chan error, 1)
	go func() { done <- h.readAgent(bytes.NewReader(agentOutput(floodFrames, floodSize))) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("agent output blocked on a stalled controller")
	}
	conn.mu.Lock()
	maxBytes, dropped := conn.maxBytes, conn.dropped
	conn.mu.Unlock()
	assert.LessOrEqual(t, maxBytes, queueBytes)
	assert.Positive(t, dropped, "overflow went to the journal")

	assert.Equal(t, want(floodFrames), readNumbers(t, ctrlEnd, floodFrames))
}

// TestBackpressureNoController: with no controller for the whole run the
// agent is not blocked and nothing is buffered; a controller connecting
// with since=0 gets every frame from the journal in order.
func TestBackpressureNoController(t *testing.T) {
	h := testHost(t)
	require.NoError(t, h.readAgent(bytes.NewReader(agentOutput(floodFrames, floodSize))))
	assert.Empty(t, h.agentReqs)
	assert.Empty(t, h.turnEnds)

	hostEnd, ctrlEnd := socketPair(t)
	go h.serveConn(hostEnd)
	since := int64(0)
	hb, _ := json.Marshal(Hello{Protocol: Protocol, Since: &since})
	_, err := ctrlEnd.Write(append(hb, '\n'))
	require.NoError(t, err)
	rd := bufio.NewReaderSize(ctrlEnd, 1<<20)
	line, err := rd.ReadBytes('\n')
	require.NoError(t, err)
	var w Welcome
	require.NoError(t, json.Unmarshal(line, &w))
	assert.Equal(t, int64(floodFrames), w.Seq)
	assert.Equal(t, want(floodFrames), readNumbers(t, rd, floodFrames))
}

// TestAgentRequestBound: agent requests awaiting a controller are capped;
// extras are refused with an error to the agent instead of held.
func TestAgentRequestBound(t *testing.T) {
	h := testHost(t)
	pr, pw := io.Pipe()
	h.agentIn = pw
	refused := make(chan []byte, 1)
	go func() {
		b, _ := bufio.NewReader(pr).ReadBytes('\n')
		refused <- b
	}()
	var b bytes.Buffer
	for i := range maxAgentReqs + 1 {
		fmt.Fprintf(&b, `{"jsonrpc":"2.0","id":%d,"method":"session/request_permission","params":{}}`+"\n", i)
	}
	require.NoError(t, h.readAgent(&b))
	assert.Len(t, h.agentReqs, maxAgentReqs)
	select {
	case line := <-refused:
		assert.Contains(t, string(line), fmt.Sprintf(`"id":%d`, maxAgentReqs))
		assert.Contains(t, string(line), "error")
	case <-time.After(5 * time.Second):
		t.Fatal("extra request not refused")
	}
}

func socketPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s")
	if len(path) > 100 {
		d, err := os.MkdirTemp("", "js")
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.RemoveAll(d) })
		path = filepath.Join(d, "s")
	}
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	a, err := net.Dial("unix", path)
	require.NoError(t, err)
	b, err := ln.Accept()
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return b, a
}
