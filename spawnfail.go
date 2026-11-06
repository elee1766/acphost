package acphost

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// stderrTap copies the sandboxed agent's stderr to the host log and
// watches it for bwrap's --json-status-fd lines. bwrap writes
// {"exit-code": n} only after the command it ran exited, so a sandbox
// exit without that line means setup failed before the agent ran (#100).
// The last plain line is kept as the setup error.
type stderrTap struct {
	done chan struct{}

	mu         sync.Mutex
	ranAgent   bool
	helperFail string // a line from the userns or pdeath helper's fail
	last       string
}

func newStderrTap(dst io.Writer) (*stderrTap, *os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	t := &stderrTap{done: make(chan struct{})}
	go func() {
		defer close(t.done)
		defer func() { _ = r.Close() }()
		br := bufio.NewReaderSize(r, 64<<10)
		for {
			line, err := br.ReadBytes('\n')
			if len(line) > 0 {
				_, _ = dst.Write(line)
				t.observe(bytes.TrimSpace(line))
			}
			if err != nil {
				return
			}
		}
	}()
	return t, w, nil
}

func (t *stderrTap) observe(line []byte) {
	if len(line) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if line[0] == '{' {
		var st struct {
			ExitCode *int `json:"exit-code"`
		}
		if json.Unmarshal(line, &st) == nil {
			if st.ExitCode != nil {
				t.ranAgent = true
			}
			return
		}
	}
	if len(line) > 500 {
		line = line[:500]
	}
	if bytes.HasPrefix(line, []byte("acphost userns: ")) || bytes.HasPrefix(line, []byte("acphost pdeath: ")) || bytes.HasPrefix(line, []byte("nsenter: ")) {
		t.helperFail = string(line)
	}
	t.last = string(line)
}

// tapResult is what the tap saw by the time the host stopped waiting.
type tapResult struct {
	ranAgent   bool
	helperFail string
	last       string
}

// wait returns what t saw once stderr is closed or after d, whichever is
// first. A process that kept stderr open past d only loses later lines.
func (t *stderrTap) wait(d time.Duration) tapResult {
	select {
	case <-t.done:
	case <-time.After(d):
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return tapResult{t.ranAgent, t.helperFail, t.last}
}

// setupError returns the spawn setup error the tap saw, or "". sandboxed
// says bwrap ran, code is the exit code of the started process.
func (r tapResult) setupError(sandboxed bool, code int) string {
	if code == 0 {
		return ""
	}
	if r.helperFail != "" {
		return r.helperFail
	}
	if sandboxed && !r.ranAgent {
		if r.last != "" {
			return r.last
		}
		return fmt.Sprintf("sandbox exited %d before the agent ran", code)
	}
	return ""
}

// statusWrap puts a shell in front of bwrap in argv that opens fd 3 as
// a copy of stderr, for --json-status-fd 3. It goes right before bwrap
// because pasta closes inherited fds other than 0-2, and on stderr
// itself bwrap would close the agent's stderr. bwrap is the element
// before --die-with-parent, which sandboxArgs always writes first.
func statusWrap(argv []string) ([]string, error) {
	i := slices.Index(argv, "--die-with-parent") - 1
	if i < 0 {
		return nil, errors.New("sandbox: bwrap not found in argv")
	}
	sh, err := lookPath("sh")
	if err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	out := slices.Clone(argv[:i])
	out = append(out, sh, "-c", `exec "$@" 3>&2`, "acphost-status")
	return append(out, argv[i:]...), nil
}

// spawnFailure returns a SpawnError when dir holds an exit.json with
// ReasonSpawnFailed that is newer than spec.json and no host holds the
// lock, so a client that cannot reach the socket learns the host gave
// up instead of retrying until its own timeout.
func spawnFailure(dir string) error {
	ex, ok := ReadExit(dir)
	if !ok || ex.Reason != ReasonSpawnFailed {
		return nil
	}
	es, err1 := os.Stat(filepath.Join(dir, ExitFile))
	ss, err2 := os.Stat(filepath.Join(dir, SpecFile))
	if err1 != nil || err2 != nil || es.ModTime().Before(ss.ModTime()) {
		return nil // exit.json from a run before this spec
	}
	if Alive(dir) {
		return nil
	}
	msg := ex.Error
	if msg == "" {
		msg = "host gave no reason"
	}
	return &SpawnError{Exit: ex, Err: errors.New(msg)}
}
