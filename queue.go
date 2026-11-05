package acphost

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// Bounds on what the host buffers for a controller. Agent notifications
// past the bound are dropped from memory only: they are already in the
// journal and the writer replays them from there, in seq order, once the
// controller catches up. Responses, agent requests and host messages are
// never dropped; they are bounded by in-flight controller calls and by
// maxAgentReqs.
const maxAgentReqs = 256

// Queue bounds. Variables so tests can overflow them with little data.
var (
	queueBytes  = 8 << 20
	queueFrames = 4096
)

// outItem is one line waiting for the controller.
type outItem struct {
	line []byte
	seq  int64 // journal seq; 0 for lines not in the journal
	end  int64 // journal offset after this frame
	drop bool  // a notification: may be dropped and replayed from the journal
}

// controllerConn is one controller connection. Only its writer goroutine
// writes to nc, so a controller that stops reading blocks that goroutine
// and nothing else.
type controllerConn struct {
	gen int64
	nc  net.Conn

	mu     sync.Mutex
	cond   *sync.Cond
	q      []outItem
	qBytes int
	closed bool
	// Journal cursor: the last frame this controller has been given.
	sentSeq, sentOff int64
	// gapSeq is the highest dropped seq and gapOff the journal offset after
	// it. gapSeq > sentSeq means frames must be replayed from the journal.
	gapSeq, gapOff int64
	dropped        int64 // frames dropped since the last replay, for the log
	maxBytes       int   // high-water mark of qBytes, for tests
}

func newConn(gen int64, nc net.Conn, seq, off int64) *controllerConn {
	c := &controllerConn{gen: gen, nc: nc, sentSeq: seq, sentOff: off}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// send queues a line without blocking. Callers sending journaled frames
// hold Host.mu, so queued seqs are increasing.
func (c *controllerConn) send(it outItem) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if it.drop && (len(c.q) >= queueFrames || c.qBytes+len(it.line) > queueBytes) {
		c.gapSeq, c.gapOff = it.seq, it.end
		c.dropped++
		c.cond.Signal()
		return
	}
	c.q = append(c.q, it)
	c.qBytes += len(it.line)
	c.maxBytes = max(c.maxBytes, c.qBytes)
	c.cond.Signal()
}

func (c *controllerConn) close() {
	c.mu.Lock()
	c.closed = true
	c.q, c.qBytes = nil, 0
	c.cond.Broadcast()
	c.mu.Unlock()
	_ = c.nc.Close()
}

// writeLoop delivers queued lines and journal replays until the
// connection closes or a write fails.
func (h *Host) writeLoop(c *controllerConn) {
	w := bufio.NewWriterSize(c.nc, 64<<10)
	for {
		c.mu.Lock()
		for len(c.q) == 0 && c.gapSeq <= c.sentSeq && !c.closed {
			c.cond.Wait()
		}
		if c.closed {
			c.mu.Unlock()
			return
		}
		from, fromOff := c.sentSeq, c.sentOff
		if len(c.q) == 0 {
			// Caught up on live frames; fill the gap from the journal.
			to, toOff, n := c.gapSeq, c.gapOff, c.dropped
			c.dropped = 0
			c.mu.Unlock()
			if err := h.replay(w, fromOff, from, to); err != nil {
				h.dropConn(c, err)
				return
			}
			if n > 0 {
				h.log.Info("replayed frames from journal", "gen", c.gen, "dropped", n, "through_seq", to)
			}
			c.mu.Lock()
			c.sentSeq, c.sentOff = to, toOff
			c.mu.Unlock()
			continue
		}
		it := c.q[0]
		c.q[0] = outItem{}
		c.q = c.q[1:]
		c.qBytes -= len(it.line)
		gap := it.seq > 0 && c.gapSeq > c.sentSeq && it.seq-1 > c.sentSeq
		more := len(c.q) > 0
		c.mu.Unlock()
		if gap {
			// Dropped frames older than this one go first.
			if err := h.replay(w, fromOff, from, it.seq-1); err != nil {
				h.dropConn(c, err)
				return
			}
		}
		_, err := w.Write(append(it.line, '\n'))
		if err == nil && !more {
			err = w.Flush()
		}
		if err != nil {
			h.dropConn(c, err)
			return
		}
		if it.seq > 0 {
			c.mu.Lock()
			c.sentSeq, c.sentOff = it.seq, it.end
			c.mu.Unlock()
		}
	}
}

// replay writes the journal's agent notifications with from < seq <= to,
// reading from byte offset off (the end of frame from).
func (h *Host) replay(w *bufio.Writer, off, from, to int64) error {
	if to <= from {
		return nil
	}
	f, err := os.Open(filepath.Join(h.dir, JournalFile))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return err
	}
	br := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		var fr Frame
		if json.Unmarshal(line, &fr) != nil || fr.Seq <= from {
			continue
		}
		if fr.Seq > to {
			break
		}
		if fr.From == "agent" && isNotification(fr.Msg) {
			if _, err := w.Write(append(bytes.TrimSpace(fr.Msg), '\n')); err != nil {
				return err
			}
		}
	}
	return w.Flush()
}

func isNotification(msg []byte) bool {
	var f frame
	return json.Unmarshal(msg, &f) == nil && f.Method != "" && len(f.ID) == 0
}

// journalOffset returns the byte offset just after frame seq (0 for seq 0
// or a seq not found).
func journalOffset(path string, seq int64) int64 {
	if seq <= 0 {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	var found int64
	_, _ = scanFramesOff(f, func(fr Frame, end int64) {
		if fr.Seq <= seq {
			found = end
		}
	})
	return found
}

func scanFramesOff(f *os.File, fn func(Frame, int64)) (int64, error) {
	br := bufio.NewReaderSize(f, 1<<20)
	var off int64
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return off, nil
			}
			return off, err
		}
		off += int64(len(line))
		var fr Frame
		if json.Unmarshal(line, &fr) == nil {
			fn(fr, off)
		}
	}
}
