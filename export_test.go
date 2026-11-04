package acphost

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeExporter struct {
	mu     sync.Mutex
	frames []string // from + " " + line
	takes  int
	closed bool
}

func (e *fakeExporter) Observe(from string, line []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.frames = append(e.frames, from+" "+string(line))
}

// TakeCounts reports 3 dropped and 1 truncated once, then nothing.
func (e *fakeExporter) TakeCounts() (int64, int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.takes++
	if e.takes == 1 {
		return 3, 1
	}
	return 0, 0
}

func (e *fakeExporter) Close(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	return nil
}

func TestExporter(t *testing.T) {
	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	// The agent sends one notification, waits for one controller line,
	// then lingers long enough for a stats report.
	script := "#!/bin/sh\necho '{\"jsonrpc\":\"2.0\",\"method\":\"hi\"}'\nread l\nsleep 1\n"
	require.NoError(t, os.WriteFile(agent, []byte(script), 0o700))
	require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol, Session: "s1", Command: agent, Dir: dir, GCRootDir: dir}))

	exp := &fakeExporter{}
	var gotSpec Spec
	done := make(chan error, 1)
	go func() {
		done <- Main(dir,
			WithExporter(func(s Spec) (Exporter, error) { gotSpec = s; return exp, nil }),
			func(o *options) { o.statsEvery = 50 * time.Millisecond })
	}()

	var nc net.Conn
	require.Eventually(t, func() bool {
		var err error
		nc, err = net.Dial("unix", filepath.Join(dir, ACPSocket))
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	defer func() { _ = nc.Close() }()
	hello, _ := json.Marshal(Hello{Protocol: Protocol})
	_, err := nc.Write(append(hello, '\n'))
	require.NoError(t, err)
	rd := bufio.NewReader(nc)
	_, err = rd.ReadBytes('\n') // welcome
	require.NoError(t, err)
	_, err = nc.Write([]byte(`{"jsonrpc":"2.0","method":"ping"}` + "\n"))
	require.NoError(t, err)

	_ = nc.SetReadDeadline(time.Now().Add(5 * time.Second))
	var stats ExportStats
	for {
		line, err := rd.ReadBytes('\n')
		require.NoError(t, err)
		var m struct {
			Method string      `json:"method"`
			Params ExportStats `json:"params"`
		}
		if json.Unmarshal(line, &m) == nil && m.Method == MethodExportStats {
			stats = m.Params
			break
		}
	}
	assert.Equal(t, ExportStats{Dropped: 3, Truncated: 1}, stats)

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("host did not exit")
	}
	exp.mu.Lock()
	defer exp.mu.Unlock()
	assert.Equal(t, "s1", gotSpec.Session)
	assert.True(t, exp.closed, "Close called")
	assert.Contains(t, exp.frames, `agent {"jsonrpc":"2.0","method":"hi"}`)
	assert.Contains(t, exp.frames, `controller {"jsonrpc":"2.0","method":"ping"}`)
}
