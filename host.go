package acphost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// maxFrame bounds one NDJSON frame; agents can send large tool output.
const maxFrame = 64 << 20

// frame is the minimal view of a JSON-RPC message the host needs.
type frame struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

func (f frame) isRequest() bool  { return f.Method != "" && len(f.ID) > 0 }
func (f frame) isResponse() bool { return f.Method == "" }

// pendingCall is a controller request forwarded to the agent under a
// host-assigned id.
type pendingCall struct {
	gen    int64           // controller connection that sent it
	origID json.RawMessage // id the controller used
	method string
}

// Host runs one session.
type Host struct {
	dir  string
	spec Spec
	log  *slog.Logger
	// cgroup is the session cgroup dir, empty without Spec.Cgroup.
	cgroup string
	// oomStart is memory.events oom_kill when the cgroup was prepared.
	oomStart int64

	journal *os.File
	conv    Exporter // nil unless the caller supplied an ExporterFactory
	opts    options
	agentIn io.WriteCloser
	agent   *exec.Cmd
	inMu    sync.Mutex // serializes writes to the agent

	mu           sync.Mutex
	seq          int64
	off          int64 // journal size, the offset after frame seq
	lastStart    int64 // offset of frame seq
	idxSeq       int64 // seq of the last index record
	index        *os.File
	dropped      int64 // agent requests refused at maxAgentReqs
	nextID       int64
	gen          int64
	conn         *controllerConn
	disconnected time.Time
	calls        map[string]pendingCall     // host id -> controller call
	agentReqs    map[string]json.RawMessage // agent request id -> frame, until answered
	tap          *stderrTap                 // sandbox stderr reader, nil without Spec.Sandbox
	initResult   json.RawMessage
	turnEnds     [][]byte // turn_end notifications waiting for a controller
	prompts      int      // session/prompt calls in flight

	stopOnce   sync.Once
	stopping   chan struct{}
	stopReason string // first reason passed to stop, written once inside stopOnce
}

// Main runs the host for the session directory dir until the agent exits.
// It is the body of the caller's host subcommand (`jobshop session-host`).
func Main(dir string, opts ...Option) error {
	o := options{statsEvery: 10 * time.Second}
	for _, opt := range opts {
		opt(&o)
	}
	// Pdeathsig fires when the thread that started the child exits, so the
	// agent is started from this locked thread, which lives until Main returns.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	spec, err := ReadSpec(dir)
	if err != nil {
		return err
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := os.Stat(filepath.Join(dir, CompressedJournalFile)); err == nil {
		if _, err := os.Stat(filepath.Join(dir, JournalFile)); errors.Is(err, os.ErrNotExist) {
			return ErrJournalCompressed
		}
	}
	if err := os.WriteFile(filepath.Join(dir, PidFile), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(dir, ExitFile))
	// Orphans of the agent reparent to the host and die with its group.
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		log.Warn("subreaper", "err", err)
	}
	h := &Host{
		dir: dir, spec: spec, log: log.With("session", spec.Session), opts: o,
		calls: map[string]pendingCall{}, agentReqs: map[string]json.RawMessage{},
		stopping: make(chan struct{}), disconnected: time.Now(),
	}
	return h.run()
}

func lockDir(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, LockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another host owns %s: %w", dir, err)
	}
	return func() { _ = f.Close() }, nil
}

func (h *Host) run() error {
	var err error
	h.journal, err = os.OpenFile(filepath.Join(h.dir, JournalFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = h.journal.Close() }()
	jpath := filepath.Join(h.dir, JournalFile)
	if s, err := openSource(jpath); err == nil {
		// Bring the index up to date (a crash may have left it behind).
		if ix, err := ensureIndex(s, jpath); err == nil {
			h.seq = ix.lastSeq
			// ensureIndex does not write while a host holds the lock, and
			// this host does, so write the rebuilt or extended index here.
			if err := writeIndex(idxPath(jpath), ix.recs); err != nil {
				return err
			}
			if n := len(ix.recs); n > 0 {
				h.idxSeq = ix.recs[n-1].seq
			}
		}
		_ = s.Close()
	}
	h.index, err = os.OpenFile(idxPath(jpath), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		h.mu.Lock()
		if h.seq > h.idxSeq {
			h.writeIdx(h.seq, h.lastStart)
		}
		h.mu.Unlock()
		_ = h.index.Close()
	}()
	if exe, err := os.Executable(); err == nil {
		defer registerGCRoot(h.log, h.spec, exe)()
	}
	if st, err := h.journal.Stat(); err == nil {
		h.off = st.Size()
	}

	if err := CheckSocketPath(h.dir); err != nil {
		return err
	}
	sockPath := SocketPath(h.dir)
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close(); _ = os.Remove(sockPath) }()
	if err := os.Chmod(sockPath, 0o600); err != nil {
		return err
	}

	if f := h.opts.exporter; f != nil {
		// Export is best effort: a setup error is logged and the session runs.
		h.conv, err = f(h.spec)
		if err != nil {
			h.log.Warn("conversation export disabled", "err", err)
			h.conv = nil
		}
	}

	agentOut, err := h.startAgent()
	if err != nil {
		if h.cgroup != "" {
			_ = removeCgroup(h.cgroup)
		}
		ex := Exit{Reason: ReasonSpawnFailed, ExitCode: -1, Error: err.Error()}
		h.writeExit(ex)
		return &SpawnError{Exit: ex, Err: err}
	}
	h.log.Info("agent started", "pid", h.agent.Process.Pid, "command", h.spec.Command)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	signal.Ignore(syscall.SIGHUP, syscall.SIGPIPE)
	go func() {
		s := <-sigs
		h.stop("signal " + s.String())
	}()
	go h.acceptLoop(ln)
	go h.leaseLoop()
	if h.conv != nil || h.cgroup != "" {
		go h.statsLoop()
	}

	readErr := h.readAgent(agentOut) // returns when the agent closes stdout
	waitErr := h.agent.Wait()
	h.stop("agent exited")
	// Kill anything the agent left in its group (it was the group leader)
	// or, with a cgroup, anywhere in the cgroup.
	_ = unix.Kill(-h.agent.Process.Pid, unix.SIGKILL)
	if h.cgroup != "" {
		h.killAll()
		if !waitCgroupEmpty(h.cgroup, 5*time.Second) {
			h.log.Warn("cgroup still populated", "cgroup", h.cgroup)
		}
	}

	// The group is dead, so stderr is closed unless something left it.
	var tap tapResult
	if h.tap != nil {
		tap = h.tap.wait(2 * time.Second)
	}

	// stop has run by now, so stopReason is set. It is "agent exited"
	// unless the host stopped the agent first (controller stop, lease).
	ex := Exit{Reason: h.stopReason, ExitCode: h.agent.ProcessState.ExitCode()}
	if ws, ok := h.agent.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		ex.Signal = ws.Signal().String()
	} else if h.spec.Sandbox != nil && ex.ExitCode > 128 && ex.ExitCode < 128+65 {
		// bwrap passes the agent's exit code through and reports a
		// signal death as 128+n. Report it the way an unsandboxed
		// agent's signal death is reported.
		ex.Signal = syscall.Signal(ex.ExitCode - 128).String()
		ex.ExitCode = -1
	}
	// bwrap writes an exit-code status line once the agent has run, and
	// the userns and pdeath helpers prefix their errors. A non-zero exit
	// the host did not cause, with no status line or with a helper error,
	// is a setup failure: a bad bwrap argument or mount, a missing
	// command, pasta or the deny rules failing, or setresuid refused.
	var spawnErr *SpawnError
	if h.tap != nil && ex.Reason == "agent exited" && ex.Signal == "" {
		if msg := tap.setupError(h.spec.Sandbox != nil, ex.ExitCode); msg != "" {
			ex.Reason, ex.Error = ReasonSpawnFailed, msg
			spawnErr = &SpawnError{Err: errors.New(msg)}
		}
	}
	if readErr != nil && ex.Reason == "agent exited" {
		ex.Reason = "agent output: " + readErr.Error()
	}
	if h.cgroup != "" {
		st := readCgroupStats(h.cgroup)
		ex.OOMKill = st.OOMKills > 0
		ex.Cgroup = &st
		ex.Reason = oomReason(ex.Reason, h.oomStart, st.OOMKills)
		if err := removeCgroup(h.cgroup); err != nil {
			h.log.Warn("remove cgroup", "err", err)
		}
	}
	h.writeExit(ex)
	if h.conv != nil {
		// Bounded: a dead endpoint must not hold the host open.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.conv.Close(ctx); err != nil {
			h.log.Warn("conversation export flush", "err", err)
		}
		cancel()
	}
	h.log.Info("host exiting", "exit", ex, "wait", waitErr)
	if spawnErr != nil {
		spawnErr.Exit = ex
		return spawnErr
	}
	return nil
}

func (h *Host) startAgent() (io.Reader, error) {
	cmd := exec.Command(h.spec.Command, h.spec.Args...)
	// iso records what this function applies, from the code that applies
	// it. Written once the agent has started.
	var iso Isolation
	// pdeathEnv selects the pdeath helper for pasta (see pdeathChildEnv).
	var pdeathEnv string
	if h.spec.Sandbox != nil {
		sb := sandboxWithEnv(*h.spec.Sandbox, h.spec.Env)
		// The status lines tell a bwrap setup failure from an agent exit
		// (see stderrTap). bwrap closes the status fd in the sandbox, so
		// it is a dup of stderr made just for bwrap (statusWrap).
		sb.ExtraArgs = append([]string{"--json-status-fd", "3"}, sb.ExtraArgs...)
		argv, net, err := sandboxArgs(sb, h.spec.Command, h.spec.Args, h.spec.Dir)
		if err != nil {
			return nil, err
		}
		if argv, err = statusWrap(argv); err != nil {
			return nil, err
		}
		iso.Bwrap, iso.Network = argv, net
		if net != nil && net.Mode == "pasta" {
			argv, pdeathEnv = pdeathCommand(argv)
		}
		// bwrap does the chdir (Dir is a path inside the sandbox).
		cmd = exec.Command(argv[0], argv[1:]...)
	} else {
		cmd.Dir = h.spec.Dir
	}
	cmd.Env = append(os.Environ(), h.spec.Env...)
	if pdeathEnv != "" {
		cmd.Env = append(cmd.Env, pdeathEnv)
	}
	cmd.Stderr = os.Stderr // host.log
	if h.spec.Sandbox != nil || h.spec.User != nil {
		tap, w, err := newStderrTap(os.Stderr)
		if err != nil {
			return nil, err
		}
		cmd.Stderr = w
		// The child holds its own copy once started.
		defer func() { _ = w.Close() }()
		h.tap = tap
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if h.spec.Cgroup != nil {
		dir, limits, err := prepareCgroup(*h.spec.Cgroup, h.spec.Session)
		if dir != "" {
			h.cgroup = dir
			h.oomStart = oomKills(dir)
		}
		if err != nil {
			return nil, err
		}
		iso.Cgroup = &CgroupApplied{Dir: dir, Limits: limits}
		f, err := os.Open(dir)
		if err != nil {
			return nil, err
		}
		// clone3 places the child in the cgroup before it runs, so
		// nothing it forks can escape. Closed once started.
		defer func() { _ = f.Close() }()
		cmd.SysProcAttr.UseCgroupFD = true
		cmd.SysProcAttr.CgroupFD = int(f.Fd())
	}
	start := cmd.Start
	if h.spec.User != nil {
		var err error
		if start, iso.User, err = usernsCommand(cmd, *h.spec.User); err != nil {
			return nil, err
		}
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := start(); err != nil {
		return nil, err
	}
	// pasta runs its command only after configuring the namespace, so a
	// started process here means pasta started; a pasta failure later
	// shows as the agent's exit.
	if iso.Network != nil && iso.Network.Mode == "pasta" {
		iso.Network.PastaStarted = true
	}
	h.writeIsolation(iso)
	h.agent, h.agentIn = cmd, in
	return out, nil
}

// stop asks the agent to exit: SIGTERM to its group, SIGKILL after 5s.
func (h *Host) stop(reason string) {
	h.stopOnce.Do(func() {
		h.stopReason = reason
		close(h.stopping)
		h.log.Info("stopping", "reason", reason)
		if h.agent == nil || h.agent.Process == nil {
			return
		}
		pid := h.agent.Process.Pid
		_ = h.agentIn.Close()
		_ = unix.Kill(-pid, unix.SIGTERM)
		time.AfterFunc(5*time.Second, h.killAll)
	})
}

// killAll SIGKILLs the agent's cgroup when it has one with cgroup.kill,
// which also reaches processes that left the process group, else its
// process group.
func (h *Host) killAll() {
	if h.cgroup != "" && cgroupKill(h.cgroup) {
		return
	}
	_ = unix.Kill(-h.agent.Process.Pid, unix.SIGKILL)
}

func (h *Host) writeIsolation(iso Isolation) {
	b, _ := json.Marshal(iso) // fixed struct, cannot fail
	if err := writeFileAtomic(filepath.Join(h.dir, IsolationFile), b); err != nil {
		h.log.Error("write isolation", "err", err)
	}
}

func (h *Host) writeExit(e Exit) {
	b, _ := json.Marshal(e) // fixed struct, cannot fail
	if err := os.WriteFile(filepath.Join(h.dir, ExitFile), b, 0o600); err != nil {
		h.log.Error("write exit", "err", err)
	}
}

// statsLoop reports conversation export drop and truncation counts to the
// connected controller, which owns the metrics. Counts taken while no
// controller is connected are kept for the next report. With a cgroup it
// also sends the current CgroupStats, which are not kept between ticks.
func (h *Host) statsLoop() {
	t := time.NewTicker(h.opts.statsEvery)
	defer t.Stop()
	var pending ExportStats
	for {
		select {
		case <-h.stopping:
			return
		case <-t.C:
		}
		if h.conv != nil {
			d, tr := h.conv.TakeCounts()
			pending.Dropped += d
			pending.Truncated += tr
			if d > 0 {
				h.log.Warn("conversation export dropped records", "dropped", d)
			}
		}
		h.mu.Lock()
		conn := h.conn
		h.mu.Unlock()
		if conn == nil {
			continue
		}
		if h.cgroup != "" {
			conn.send(outItem{line: notification(h.spec.method("cgroup_stats"), readCgroupStats(h.cgroup))})
		}
		if pending != (ExportStats{}) {
			conn.send(outItem{line: notification(h.spec.method("export_stats"), pending)})
			pending = ExportStats{}
		}
	}
}

func (h *Host) leaseLoop() {
	lease := time.Duration(h.spec.LeaseMS) * time.Millisecond
	if lease <= 0 {
		return
	}
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-h.stopping:
			return
		case <-t.C:
		}
		h.mu.Lock()
		orphaned := h.conn == nil && time.Since(h.disconnected) > lease
		h.mu.Unlock()
		if orphaned {
			h.stop("no controller for " + lease.String())
			return
		}
	}
}

// record appends a frame to the journal. Holding h.mu.
func (h *Host) record(from string, line []byte) {
	h.seq++
	if r := h.opts.redactor; r != nil {
		line = r(from, line)
		if !json.Valid(line) {
			h.log.Warn("redactor returned invalid JSON, journaling null", "seq", h.seq, "from", from)
			line = []byte("null")
		}
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, `{"seq":%d,"t":%d,"from":%q,"msg":`, h.seq, time.Now().UnixMilli(), from)
	b.Write(line)
	b.WriteString("}\n")
	start := h.off
	n, err := h.journal.Write(b.Bytes())
	h.off += int64(n)
	if err == nil {
		h.lastStart = start
		if h.idxSeq == 0 || h.seq-h.idxSeq >= IndexEvery {
			h.writeIdx(h.seq, start)
		}
	}
	if err != nil {
		h.log.Error("journal", "err", err)
	}
}

// writeIdx appends an index record. Holding h.mu.
func (h *Host) writeIdx(seq, off int64) {
	if _, err := h.index.Write(appendIdx(nil, idxRec{seq, off})); err != nil {
		h.log.Error("journal index", "err", err)
		return
	}
	h.idxSeq = seq
}

// JournalTail returns the last sequence number and its time in dir's
// journal, or zero values if there is none.
func JournalTail(dir string) (int64, time.Time) {
	return journalTail(filepath.Join(dir, JournalFile))
}

func (h *Host) writeAgent(line []byte) error {
	h.inMu.Lock()
	defer h.inMu.Unlock()
	_, err := h.agentIn.Write(append(line, '\n'))
	return err
}

func setID(line []byte, id json.RawMessage) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return nil, err
	}
	m["id"] = id
	return json.Marshal(m)
}

func notification(method string, params any) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	return b
}

// readAgent handles every frame the agent writes.
func (h *Host) readAgent(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), maxFrame)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		line = bytes.Clone(line)
		var f frame
		if err := json.Unmarshal(line, &f); err != nil {
			h.log.Warn("agent wrote non-JSON", "line", truncate(line))
			continue
		}
		// Journal first, then a non-blocking queue send: a slow or absent
		// controller never stalls the agent's stdout.
		h.mu.Lock()
		h.record("agent", line)
		if h.conv != nil {
			h.conv.Observe("agent", line)
		}
		out, conn := h.routeAgent(f, line)
		if out != nil && conn != nil {
			// Every agent frame carries its seq so dropped notifications
			// before it are replayed first; only notifications may drop.
			conn.send(outItem{line: out, seq: h.seq, end: h.off, drop: isNotificationFrame(f) && bytes.Equal(out, line)})
		}
		h.mu.Unlock()
	}
	return sc.Err()
}

func isNotificationFrame(f frame) bool { return f.Method != "" && len(f.ID) == 0 }

// routeAgent decides where an agent frame goes. Holding h.mu.
func (h *Host) routeAgent(f frame, line []byte) ([]byte, *controllerConn) {
	switch {
	case f.isResponse():
		key := string(f.ID)
		call, ok := h.calls[key]
		if !ok {
			h.log.Warn("agent response to unknown id", "id", key)
			return nil, nil
		}
		delete(h.calls, key)
		if call.method == "initialize" && len(f.Result) > 0 {
			h.initResult = f.Result
		}
		if call.method == "session/prompt" {
			h.prompts--
		}
		if h.conn != nil && h.conn.gen == call.gen {
			out, err := setID(line, call.origID)
			if err != nil {
				return nil, nil
			}
			return out, h.conn
		}
		if call.method == "session/prompt" {
			// The controller that asked is gone; tell whoever is next.
			te := notification(h.spec.method("turn_end"), TurnEnd{Result: f.Result, Error: f.Error})
			if h.conn != nil {
				return te, h.conn
			}
			h.turnEnds = append(h.turnEnds, te)
		}
		return nil, nil
	case f.isRequest():
		if len(h.agentReqs) >= maxAgentReqs {
			// Refuse rather than hold without bound; the agent sees an error
			// for this request and stays unblocked.
			h.dropped++
			h.log.Warn("too many agent requests awaiting a controller", "id", string(f.ID), "refused", h.dropped)
			msg, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": f.ID,
				"error": map[string]any{"code": -32000, "message": "session host: too many requests awaiting the controller"},
			})
			go func() { _ = h.writeAgent(msg) }()
			return nil, nil
		}
		// Held until a controller answers, and re-sent to a new controller.
		h.agentReqs[string(f.ID)] = line
		return line, h.conn
	default:
		// Notifications (session/update) go to the live controller; they are
		// in the journal either way, so nothing is lost while detached.
		return line, h.conn
	}
}

func (h *Host) acceptLoop(ln net.Listener) {
	for {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		go h.serveConn(nc)
	}
}

// serveConn runs one controller connection: hello, welcome, then ACP.
// A new connection replaces the previous one.
func (h *Host) serveConn(nc net.Conn) {
	defer func() { _ = nc.Close() }()
	rd := bufio.NewReaderSize(nc, 1<<20)
	_ = nc.SetReadDeadline(time.Now().Add(10 * time.Second))
	helloLine, err := rd.ReadBytes('\n')
	if err != nil {
		return
	}
	_ = nc.SetReadDeadline(time.Time{})
	var hello Hello
	if err := json.Unmarshal(helloLine, &hello); err != nil || hello.Protocol != Protocol {
		h.log.Warn("bad hello", "line", truncate(helloLine))
		return
	}

	h.mu.Lock()
	old := h.conn
	h.gen++
	// Live frames start after the current seq. A controller that names an
	// earlier seq gets the agent notifications since then from the journal
	// first, through the same gap path an overflow uses.
	conn := newConn(h.gen, nc, h.seq, h.off)
	if hello.Since != nil && *hello.Since < h.seq {
		conn.sentSeq = max(*hello.Since, 0)
		conn.sentOff = journalOffset(filepath.Join(h.dir, JournalFile), conn.sentSeq)
		conn.gapSeq, conn.gapOff = h.seq, h.off
	}
	h.conn = conn
	w := Welcome{
		Protocol: Protocol, Pid: os.Getpid(), AgentPid: h.agent.Process.Pid,
		Busy: h.prompts > 0, Seq: h.seq, Initialize: h.initResult,
		MethodPrefix: MethodName(h.spec.MethodPrefix, ""),
	}
	wb, _ := json.Marshal(w)
	// The welcome goes out before the writer starts, so it is first.
	_ = nc.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = nc.Write(append(wb, '\n'))
	_ = nc.SetWriteDeadline(time.Time{})
	if err != nil {
		h.conn = nil
		h.mu.Unlock()
		return
	}
	for _, te := range h.turnEnds {
		conn.send(outItem{line: te})
	}
	h.turnEnds = nil
	for _, req := range h.agentReqs {
		conn.send(outItem{line: req})
	}
	h.mu.Unlock()
	if old != nil {
		old.close()
	}
	h.log.Info("controller connected", "gen", conn.gen, "since", conn.sentSeq)
	go h.writeLoop(conn)

	for {
		line, err := rd.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			h.fromController(conn, line)
		}
		if err != nil {
			h.dropConn(conn, err)
			return
		}
	}
}

func (h *Host) dropConn(c *controllerConn, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn == c {
		h.conn = nil
		h.disconnected = time.Now()
		if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			h.log.Info("controller disconnected", "gen", c.gen, "err", err)
		}
	}
	c.close()
}

func (h *Host) fromController(c *controllerConn, line []byte) {
	var f frame
	if err := json.Unmarshal(line, &f); err != nil {
		h.log.Warn("controller wrote non-JSON", "line", truncate(line))
		return
	}
	if f.Method == h.spec.method("stop") {
		h.stop("controller requested stop")
		return
	}
	if f.Method == "$/cancel_request" {
		// The SDK's own cancel carries the controller's request id, which the
		// agent never saw. Turns are cancelled with session/cancel instead.
		return
	}
	h.mu.Lock()
	h.record("controller", line)
	if h.conv != nil {
		h.conv.Observe("controller", line)
	}
	var reply []byte
	switch {
	case f.isResponse():
		// Answer to an agent request.
		delete(h.agentReqs, string(f.ID))
	case f.isRequest():
		if f.Method == "initialize" && h.initResult != nil {
			// The agent is already initialized; replay its answer.
			reply, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": f.ID, "result": h.initResult})
			break
		}
		h.nextID++
		hid := fmt.Sprintf(`"h%d"`, h.nextID)
		h.calls[hid] = pendingCall{gen: c.gen, origID: f.ID, method: f.Method}
		if f.Method == "session/prompt" {
			h.prompts++
		}
		var err error
		if line, err = setID(line, json.RawMessage(hid)); err != nil {
			delete(h.calls, hid)
			h.mu.Unlock()
			return
		}
	}
	h.mu.Unlock()
	if reply != nil {
		c.send(outItem{line: reply})
		return
	}
	if err := h.writeAgent(line); err != nil {
		h.log.Warn("write to agent", "err", err)
	}
}

func truncate(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "..."
	}
	return string(b)
}

// ConnectOption configures Connect.
type ConnectOption func(*connectOptions)

type connectOptions struct {
	methodPrefix string
}

// WithMethodPrefix sets the extension method prefix the controller
// expects. It must match the host's Spec.MethodPrefix. Empty means
// DefaultMethodPrefix.
func WithMethodPrefix(p string) ConnectOption {
	return func(o *connectOptions) { o.methodPrefix = p }
}

// Connect dials a host's acp.sock and performs the hello handshake. The
// returned reader holds any bytes already buffered after the welcome line.
// It fails if the host uses a different extension method prefix. When
// the host is gone and its exit.json says ReasonSpawnFailed, the error is
// a *SpawnError, so a caller retrying until the socket appears can stop.
func Connect(ctx context.Context, dir string, opts ...ConnectOption) (net.Conn, *bufio.Reader, Welcome, error) {
	var o connectOptions
	for _, opt := range opts {
		opt(&o)
	}
	var w Welcome
	var d net.Dialer
	nc, err := d.DialContext(ctx, "unix", filepath.Join(dir, ACPSocket))
	if err != nil {
		if se := spawnFailure(dir); se != nil {
			return nil, nil, w, se
		}
		return nil, nil, w, err
	}
	fail := func(err error) (net.Conn, *bufio.Reader, Welcome, error) {
		_ = nc.Close()
		return nil, nil, w, err
	}
	hb, _ := json.Marshal(Hello{Protocol: Protocol})
	if _, err := nc.Write(append(hb, '\n')); err != nil {
		return fail(err)
	}
	_ = nc.SetReadDeadline(time.Now().Add(10 * time.Second))
	rd := bufio.NewReaderSize(nc, 1<<20)
	line, err := rd.ReadBytes('\n')
	if err != nil {
		// The socket is up before the agent starts, so a host whose
		// spawn failed closes it without a welcome.
		if se := spawnFailure(dir); se != nil {
			return fail(se)
		}
		return fail(fmt.Errorf("host welcome: %w", err))
	}
	_ = nc.SetReadDeadline(time.Time{})
	if err := json.Unmarshal(line, &w); err != nil {
		return fail(fmt.Errorf("host welcome: %w", err))
	}
	if w.Protocol != Protocol {
		return fail(fmt.Errorf("host speaks protocol %d, want %d", w.Protocol, Protocol))
	}
	if got, want := MethodName(w.MethodPrefix, ""), MethodName(o.methodPrefix, ""); got != want {
		return fail(fmt.Errorf("host uses extension method prefix %q, want %q", got, want))
	}
	return nc, rd, w, nil
}
