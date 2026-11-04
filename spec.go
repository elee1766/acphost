package acphost

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Protocol is the host protocol version. A controller refuses hosts it does
// not understand rather than guessing.
const Protocol = 1

// Files in a session directory.
const (
	SpecFile    = "spec.json"
	LockFile    = "host.lock" // flock held for the host's lifetime
	PidFile     = "host.pid"
	LogFile     = "host.log"
	ACPSocket   = "acp.sock"
	JournalFile = "journal.ndjson"
	ExitFile    = "exit.json"
)

// Extension methods between controller and host on acp.sock. Everything
// else on the socket is ACP, forwarded to or from the agent.
const (
	// MethodStop (controller to host, notification): stop the agent and exit.
	MethodStop = "_jobshop/stop"
	// MethodTurnEnd (host to controller, notification): a session/prompt
	// sent by an earlier controller connection finished.
	MethodTurnEnd = "_jobshop/turn_end"
	// MethodExportStats (host to controller, notification): conversation
	// export records dropped and fields truncated since the last report.
	MethodExportStats = "_jobshop/export_stats"
)

// ExportStats is the payload of MethodExportStats.
type ExportStats struct {
	Dropped   int64 `json:"dropped"`
	Truncated int64 `json:"truncated"`
}

// Spec is written by the controller before spawning the host.
type Spec struct {
	Protocol int      `json:"protocol"`
	Session  string   `json:"session"`
	Command  string   `json:"command"`
	Args     []string `json:"args,omitempty"`
	// Env is added to the host's environment for the agent (KEY=VALUE).
	Env []string `json:"env,omitempty"`
	// EnvHash identifies the agent's full env (inherited plus Env). The host
	// ignores it; a controller compares it to decide whether to respawn.
	EnvHash string `json:"env_hash,omitempty"`
	Dir     string `json:"dir"`
	// LeaseMS: the host exits if no controller has been connected this long.
	LeaseMS int64 `json:"lease_ms"`
	// GCRootDir overrides where the Nix GC root for the host binary goes
	// (default /nix/var/nix/gcroots/per-user/$USER). Used by tests.
	GCRootDir string `json:"gcroot_dir,omitempty"`
}

// Hello is the first line a controller sends on acp.sock.
type Hello struct {
	Protocol int `json:"protocol"`
	// Since, if set, asks for every agent notification after this journal
	// seq to be replayed before live traffic. Older hosts ignore it.
	Since *int64 `json:"since,omitempty"`
}

// Welcome is the host's reply, sent before any ACP traffic.
type Welcome struct {
	Protocol int   `json:"protocol"`
	Pid      int   `json:"pid"`
	AgentPid int   `json:"agent_pid"`
	Busy     bool  `json:"busy"` // a session/prompt is in flight
	Seq      int64 `json:"seq"`  // last journal sequence number
	// Initialize is the agent's cached initialize result, if any.
	Initialize json.RawMessage `json:"initialize,omitempty"`
}

// TurnEnd is the payload of MethodTurnEnd: the raw prompt result or error.
type TurnEnd struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

// Exit records why a host stopped.
type Exit struct {
	Reason   string `json:"reason"`
	ExitCode int    `json:"exit_code"`
	Signal   string `json:"signal,omitempty"`
}

// WriteSpec stores spec in dir.
func WriteSpec(dir string, s Spec) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, SpecFile), b, 0o600)
}

// ReadSpec loads the spec from dir.
func ReadSpec(dir string) (Spec, error) {
	var s Spec
	b, err := os.ReadFile(filepath.Join(dir, SpecFile))
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("%s: %w", SpecFile, err)
	}
	if s.Protocol != Protocol {
		return s, fmt.Errorf("%s: protocol %d, this build speaks %d", SpecFile, s.Protocol, Protocol)
	}
	return s, nil
}

// ReadExit loads exit.json if the host wrote one.
func ReadExit(dir string) (Exit, bool) {
	var e Exit
	b, err := os.ReadFile(filepath.Join(dir, ExitFile))
	if err != nil || json.Unmarshal(b, &e) != nil {
		return e, false
	}
	return e, true
}

// Alive reports whether a host holds dir's lock. It never blocks.
func Alive(dir string) bool {
	f, err := os.Open(filepath.Join(dir, LockFile))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return true
	}
	if err == nil {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	}
	return false
}
