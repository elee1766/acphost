package acphost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/elee1766/acphost/pkg/cgroup"
	"github.com/elee1766/acphost/pkg/proc"
	"github.com/elee1766/acphost/pkg/sandbox"
	"github.com/elee1766/acphost/pkg/userns"
)

// Protocol is the version of the wire format between client and host:
// spec.json, the hello and welcome lines, the "_"-prefixed extension
// methods and the journal lines. A client refuses a host, and ReadSpec a
// spec, with another value rather than guessing. A change that breaks an
// older client or host bumps it. A Go API change alone does not.
const Protocol = 1

// Files in a session directory.
const (
	SpecFile    = "spec.json"      // Spec, written by the client before start
	LogFile     = "host.log"       // host stderr, if the starter redirects it
	ACPSocket   = "acp.sock"       // unix socket clients Connect to
	JournalFile = "journal.ndjson" // one Frame per line
	ExitFile    = "exit.json"      // Exit, written when the host stops
	// IsolationFile holds Isolation, written once the agent has started.
	IsolationFile = "isolation.json"
)

// Extension methods between controller and host on acp.sock. Everything
// else on the socket is ACP, forwarded to or from the agent. The names
// below use DefaultMethodPrefix. A session with Spec.MethodPrefix set uses
// that prefix instead (see MethodName).
const (
	// DefaultMethodPrefix is the extension method prefix when
	// Spec.MethodPrefix is empty, the name hosts have always used.
	DefaultMethodPrefix = "_jobshop/"
	// DefaultGCRootPrefix is the GC root link name prefix when
	// Spec.GCRootPrefix is empty. The link is <prefix><session>.
	DefaultGCRootPrefix = "jobshop-host-"

	// MethodStop (controller to host, notification): stop the agent and exit.
	MethodStop = "_jobshop/stop"
	// MethodTurnEnd (host to controller, notification): a session/prompt
	// sent by an earlier controller connection finished.
	MethodTurnEnd = "_jobshop/turn_end"
	// MethodExportStats (host to controller, notification): conversation
	// export records dropped and fields truncated since the last report.
	MethodExportStats = "_jobshop/export_stats"
	// MethodCgroupStats (host to controller, notification): cgroup.Stats
	// of the session cgroup, sent periodically (Spec.Cgroup only).
	MethodCgroupStats = "_jobshop/cgroup_stats"
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
	// MethodPrefix replaces DefaultMethodPrefix for extension methods. The
	// controller must Connect with the same prefix.
	MethodPrefix string `json:"method_prefix,omitempty"`
	// GCRootPrefix replaces DefaultGCRootPrefix in the GC root link name.
	GCRootPrefix string `json:"gcroot_prefix,omitempty"`
	// Sandbox, if set, runs the agent inside a sandbox. Nil runs it
	// directly.
	Sandbox *sandbox.Sandbox `json:"sandbox,omitempty"`
	// User, if set, runs the agent as another uid/gid in a user
	// namespace. Nil leaves ids unchanged.
	User *userns.User `json:"user,omitempty"`
	// Cgroup, if set, gives the agent its own cgroup v2 with limits, made
	// by cgroupfs under Parent or, with Driver systemd, as a transient
	// scope the systemd manager owns. Nil touches no cgroupfs.
	Cgroup *cgroup.Cgroup `json:"cgroup,omitempty"`
	// Join maps a namespace type (user, mnt, pid, net, ipc, uts, cgroup;
	// see JoinTypes) to a namespace file the agent joins instead of
	// getting a new one: a /proc/<pid>/ns/<type> link or a bind-mounted
	// ns file (see Pin). Joining runs util-linux nsenter in front of the
	// agent (or bwrap), user first and mnt last. With pid the agent is a
	// child of nsenter, since a pid join only applies to children.
	Join map[string]string `json:"join,omitempty"`
}

// MethodName returns the extension method for prefix and a suffix such as
// "stop". An empty prefix means DefaultMethodPrefix.
func MethodName(prefix, name string) string {
	if prefix == "" {
		prefix = DefaultMethodPrefix
	}
	return prefix + name
}

func (s Spec) method(name string) string { return MethodName(s.MethodPrefix, name) }

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
	// MethodPrefix is the host's extension method prefix. Hosts from before
	// it existed leave it empty, which means DefaultMethodPrefix.
	MethodPrefix string `json:"method_prefix,omitempty"`
}

// TurnEnd is the payload of MethodTurnEnd: the raw prompt result or error.
type TurnEnd struct {
	Result json.RawMessage `json:"result,omitempty"` // JSON-RPC result, if the prompt succeeded
	Error  json.RawMessage `json:"error,omitempty"`  // JSON-RPC error, if it failed
}

// Exit records why a host stopped.
type Exit struct {
	Reason   string `json:"reason"`           // human-readable cause
	ExitCode int    `json:"exit_code"`        // agent exit code, -1 if killed by a signal
	Signal   string `json:"signal,omitempty"` // signal that killed the agent, if any
	// OOMKill is set when the session cgroup's OOM killer killed a
	// process (Spec.Cgroup only).
	OOMKill bool `json:"oom_kill,omitempty"`
	// Cgroup is the session cgroup's final usage, read just before it was
	// removed (Spec.Cgroup only).
	Cgroup *cgroup.Stats `json:"cgroup,omitempty"`
	// Error is the setup error when Reason is ReasonSpawnFailed.
	Error string `json:"error,omitempty"`
}

// ReasonSpawnFailed is Exit.Reason when the agent never ran: the command,
// cgroup, user namespace, pasta or bwrap setup failed (#100). Exit.Error
// holds the setup error.
const ReasonSpawnFailed = "spawn failed"

// SpawnError is returned by Main and Connect when the host could not
// start the agent. errors.As it to tell a setup failure from a host that
// is not up yet.
type SpawnError struct {
	Exit Exit  // exit.json as written by the host
	Err  error // the setup error
}

// Error returns "spawn failed: " and the setup error.
func (e *SpawnError) Error() string { return ReasonSpawnFailed + ": " + e.Err.Error() }

// Unwrap returns the setup error.
func (e *SpawnError) Unwrap() error { return e.Err }

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

// Alive reports whether a host holds dir's lock, proc.LockFile
// (supervisor.lock). It never blocks. It is proc's Running: the probe
// takes a shared lock, so a waiter in proc's Wait, which holds one for a
// moment after the host exits, is not taken for a host.
func Alive(dir string) bool {
	return (&proc.Detached{Dir: dir}).Running()
}
