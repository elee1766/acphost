# acphost

[![ci](https://github.com/elee1766/acphost/actions/workflows/ci.yml/badge.svg)](https://github.com/elee1766/acphost/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/elee1766/acphost.svg)](https://pkg.go.dev/github.com/elee1766/acphost)
[![Go Report Card](https://goreportcard.com/badge/github.com/elee1766/acphost)](https://goreportcard.com/report/github.com/elee1766/acphost)
[![Release](https://img.shields.io/github/v/release/elee1766/acphost?sort=semver)](https://github.com/elee1766/acphost/releases)

acphost is a Go library for the [Agent Client Protocol
(ACP)](https://agentclientprotocol.com). It runs a coding agent such as
jcode (`jcode acp`), Claude Code (via `claude-code-acp`) or Gemini CLI
(`gemini --experimental-acp`) in a detached host process, so the agent
session survives client restarts: a client that reconnects replays what
it missed from a journal.

One host process per session:

- spawns the agent and owns its process group
- proxies ACP between the agent's stdio and a unix socket (`acp.sock`)
- journals every frame to `journal.ndjson`, so a client that reconnects
  can replay what it missed
- exits when no client has connected for a lease period
- writes `exit.json` with the reason when the agent stops

A client (a controller, a CLI, a chat bridge) writes a spec into a session
directory, starts the host, and connects to the socket. If the client
restarts, it reconnects and resumes from a journal sequence number.

## Status

Pre-v1, Linux only. Extracted from
[jobshop](https://github.com/elee1766/jobshop) `pkg/host`, which uses it
to run agent sessions that survive controller restarts.

## Install

```sh
go get github.com/elee1766/acphost
```

Upgrading from v0.3: `docs/notes/68-upgrade-v0.3-v0.6.md`.

## Quick start

This needs none of the isolation features described below. It starts a
host, connects, and later reconnects asking for replay from a seq. The
same code, with error handling and output checks, is `Example` in
`example_test.go`, which `go test` runs and pkg.go.dev shows.

```go
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"

	"github.com/elee1766/acphost"
)

func main() {
	dir := "/tmp/acphost-demo" // session directory

	// 1. Write the spec.
	_ = acphost.WriteSpec(dir, acphost.Spec{
		Protocol: acphost.Protocol, Session: "demo",
		Command: "claude-code-acp", Dir: "/path/to/work", LeaseMS: 600_000,
	})

	// 2. Start the host. A real client runs its own binary detached
	// (Setsid), with a subcommand whose body is acphost.Main(dir).
	go func() { _ = acphost.Main(dir) }()

	// 3. Connect (retry until acp.sock exists). The welcome has the last
	// journal seq and the cached initialize result. Speak ACP on nc.
	nc, _, w, err := acphost.Connect(context.Background(), dir)
	if err != nil {
		panic(err)
	}
	_ = nc.Close()

	// 4. After a client restart, reconnect and ask for the agent
	// notifications journaled after a seq, replayed before live traffic.
	nc, _ = net.Dial("unix", filepath.Join(dir, acphost.ACPSocket))
	since := w.Seq
	hello, _ := json.Marshal(acphost.Hello{Protocol: acphost.Protocol, Since: &since})
	_, _ = nc.Write(append(hello, '\n'))
	line, _ := bufio.NewReader(nc).ReadBytes('\n') // welcome, then replay
	fmt.Printf("%s", line)

	// 5. Stop the host.
	_, _ = nc.Write([]byte(`{"jsonrpc":"2.0","method":"` + acphost.MethodStop + `"}` + "\n"))
}
```

## Sandbox

Set `Spec.Sandbox` to run the agent under
[bubblewrap](https://github.com/containers/bubblewrap). Nil runs it
directly, as before.

```go
home, _ := os.UserHomeDir()
spec.Sandbox = &acphost.Sandbox{
	RO:      []acphost.Mount{{Src: "/nix/store"}, {Src: "/etc"}},
	RW:      []acphost.Mount{{Src: workDir}},
	Hide:    []string{filepath.Join(home, ".ssh")},
	EnvKeep: []string{"PATH", "HOME"},
	Network: "host", // or "none"
}
```

- Always added: `--die-with-parent --unshare-pid --new-session`, a fresh
  `/proc`, `/dev` and `/tmp`, `--clearenv` (then `EnvKeep` and
  `Spec.Env` are set), and `--chdir` to `Spec.Dir`, which must be inside
  an RW mount.
- Only the agent is sandboxed. The host stays outside, so the socket,
  journal and `exit.json` work as before, and `exit.json` reports the
  agent's own exit code or signal.
- Policy is the caller's job: acphost mounts exactly what the spec lists
  and adds nothing else.
- bwrap is `Sandbox.Bwrap` if set, else `bwrap` on the host's PATH.
  `CheckSpawn` reports a missing bwrap, a missing mount source or a Dir
  outside RW as a permanent error. `SandboxArgs` returns the argv
  without running it.

## pasta network

`Sandbox.Network` is `host` (default, the host's network), `none` (no network,
`--unshare-net`) or `pasta`. With `pasta` the agent gets a private
network namespace with user-mode networking from
[passt](https://passt.top), and `Deny` lists CIDRs it cannot reach:

```go
spec.Sandbox.Network = "pasta"
spec.Sandbox.Deny = []string{"10.0.0.0/8", "169.254.169.254/32", "fd00::/8"}
```

- bwrap runs under `pasta --config-net`, so the interface and rules are
  in place before the agent starts, and pasta exits with the agent.
- Deny is an nftables table loaded inside the namespace (needs `nft` on
  PATH). Denied connects are rejected. The agent runs with
  `--cap-drop ALL`, so it cannot remove the rules.
- No inbound ports are forwarded.
- `Deny` with another network is an error. `CheckSpawn` reports a
  missing `pasta` (`Sandbox.Pasta` or PATH) or `nft` as a permanent
  error.

One-time setup (root): install the `passt` package (provides `pasta`),
`nftables` (provides `nft`) and `bubblewrap`.

DNS: on a host whose resolv.conf names a loopback resolver (such as
systemd-resolved's 127.0.0.53) the namespace cannot reach it. Set
`Sandbox.DNS` (pasta `--dns-forward`/`--dns-host`) and `Sandbox.ResolvConf`
(bound read-only over /etc/resolv.conf). acphost picks no resolver; this
is consumer config:

```go
spec.Sandbox.DNS = &acphost.DNS{Forward: "169.254.1.1", Host: "127.0.0.53"}
spec.Sandbox.ResolvConf = "/etc/acphost/resolv.conf" // "nameserver 169.254.1.1"
```

The forward address must be outside `Deny`. `acphost.CheckSandboxDNS(sb,
name)` resolves a name under pasta once at load time and returns a
permanent error naming these settings when it fails. Details in
`docs/notes/124-sandbox-dns.md`.

## User namespace

Set `Spec.User` to run the agent as another uid/gid inside a user
namespace. Ids written by the agent show up outside as `MapUID` and
`MapGID`. The host, the session dir and `acp.sock` stay outside, owned by
the host user.

```go
spec.User = &acphost.User{UID: 1000, GID: 1000, MapUID: 100000, MapGID: 2000, Groups: []int{2000}}
```

One-time setup (root):

- `newuidmap` and `newgidmap` on PATH (package `uidmap` on Debian,
  `shadow` elsewhere).
- An entry for the host user in `/etc/subuid` and `/etc/subgid` covering
  `MapUID` and `MapGID`, for example `jobshop:100000:65536` in both
  (`usermod --add-subuids 100000-165535 --add-subgids 100000-165535 jobshop`).
  Mapping to the host user's own uid and a gid it holds needs no entry.

Sharing a worktree between the host user and the agent: make a group
both belong to (the host user holds it, the agent gets it via `Groups`
or `MapGID`), `chgrp -R` the worktree to it, and set the setgid bit and
group write on its directories (`chmod -R g+rwX` and
`find . -type d -exec chmod g+s {} +`) so new files keep the group. Git
also needs `core.sharedRepository=group`.

With `Spec.Sandbox` too, bwrap runs inside the namespace and nests its
own. Map failures are permanent errors carrying the helper's stderr.

### Per-session subids

To give each session its own ids, cut the host user's subid range into
blocks with `AllocSubIDs`. It takes the first block whose lock file in
`lockDir` it can flock, so concurrent sessions never share a block and a
dead holder's block frees itself.

```go
a, err := acphost.AllocSubIDs(lockDir, uids, gids, 65536)
if err != nil { /* PermanentError when no block is free */ }
defer a.Release()
u := a.User(1000, 1000)
spec.User = &u
```

`uids` and `gids` come from `ParseSubIDs`. Use one `size` per lockDir.
`a.User` sets `MapCount` to the block size, and spawn, `CheckSpawn` and
`Preflight` then reject a block not inside one subuid/subgid entry, so
callers can drop their own range check (`docs/notes/113-subid-range.md`).
Details in `docs/notes/57-subid-alloc.md`.

## Cgroup

Set `Spec.Cgroup` to give the agent its own cgroup v2,
`<Parent>/<Session>`, with limits. Nil touches no cgroupfs.

```go
spec.Cgroup = &acphost.Cgroup{
	Parent:    "/sys/fs/cgroup/acphost",
	CPUMax:    "200000 100000", // cpu.max: 2 CPUs
	MemoryMax: "4294967296",    // memory.max: bytes or "max"
	SwapMax:   "0",             // memory.swap.max: make MemoryMax a hard cap
	PidsMax:   512,             // pids.max
}
```

JSON keys: `parent`, `cpu_max`, `memory_max`, `memory_high`, `swap_max`,
`pids_max`. Unset limits are not written.

- The agent starts in the cgroup. On stop the host writes `cgroup.kill`
  (Linux 5.14+), so processes that left the agent's process group die
  too, then removes the cgroup.
- `exit.json` has `"oom_kill": true` when the cgroup's OOM killer fired.
- `CheckSpawn` reports a relative or missing `Parent`, a bad value, or a
  limit whose controller is not in `Parent/cgroup.subtree_control` as a
  permanent error.

To turn cgroups on only when they work, call `acphost.DelegatedCgroup()`.
It returns the host's own cgroup (`Path`), whether the host may create
children in it (`Writable`) and its `Controllers`. A cgroup that holds
processes cannot enable controllers for children, so use a child of it
as `Parent`. Leave `Spec.Cgroup` nil when `Writable` is false or a needed
controller is missing.

One-time setup (root): the host user needs a delegated subtree.

- With systemd, set `Delegate=yes` on the unit that runs the host (a
  system service with `User=`, a user service, or
  `systemd-run --user --scope -p Delegate=yes <cmd>`). User managers
  delegate only `memory pids` by default. To add `cpu`, create
  `/etc/systemd/system/user@.service.d/delegate.conf` with
  `[Service]` and `Delegate=cpu cpuset io memory pids`. Inside the
  delegated cgroup `$CG`, move the host into a leaf and enable
  controllers for a sibling used as `Parent`:

  ```sh
  mkdir $CG/host $CG/agents
  echo $$ > $CG/host/cgroup.procs
  echo "+cpu +memory +pids" > $CG/cgroup.subtree_control
  echo "+cpu +memory +pids" > $CG/agents/cgroup.subtree_control
  ```

- Without systemd, at boot:

  ```sh
  echo "+cpu +memory +pids" > /sys/fs/cgroup/cgroup.subtree_control
  mkdir /sys/fs/cgroup/acphost
  echo "+cpu +memory +pids" > /sys/fs/cgroup/acphost/cgroup.subtree_control
  chown -R jobshop: /sys/fs/cgroup/acphost
  ```

  and use `/sys/fs/cgroup/acphost` as `Parent`.

### SetupDelegation

`acphost.SetupDelegation(own, controllers)` does the in-cgroup steps
above from Go. `own` is a delegated cgroup v2 directory, usually
`DelegatedCgroup().Path`. It moves the calling process into
`<own>/controller`, enables `controllers` in
`<own>/cgroup.subtree_control`, creates `<own>/sessions`, enables them
there too, and returns `<own>/sessions` for `Cgroup.Parent`:

```go
info, err := acphost.DelegatedCgroup()
if err == nil && info.Writable {
	parent, err := acphost.SetupDelegation(info.Path, []string{"cpu", "memory", "pids"})
	if err == nil {
		spec.Cgroup = &acphost.Cgroup{Parent: parent, MemoryMax: "4294967296"}
	}
}
```

- Idempotent: a repeat call (for example after the controller restarts)
  writes only what is missing and returns the same path.
- Returns a `*acphost.PermanentError` naming the file when a controller
  is not in `<own>/cgroup.controllers`, that file cannot be read, or a
  directory or file cannot be created or written (permission denied,
  read-only). Retrying will not help.

### Usage stats

With `Spec.Cgroup`, the host sends the notification
`acphost.MethodCgroupStats` (`<prefix>cgroup_stats`) to the connected
controller on each stats tick. The params are a `CgroupStats`:

| field | source |
|---|---|
| `memory_current` | `memory.current`, bytes |
| `memory_peak` | `memory.peak`, bytes (0 before Linux 5.19) |
| `cpu_usage_usec` | `cpu.stat` usage_usec |
| `oom_kills` | `memory.events` oom_kill |
| `memory_max_events` | `memory.events` max |

Values are kernel counters, not deltas. A missing file reads as 0. The
final read, taken just before the cgroup is removed, is stored as
`"cgroup"` in `exit.json` (`Exit.Cgroup`), next to `oom_kill`.

When the cgroup's OOM killer killed something during the session,
`Exit.Reason` is `acphost.ReasonOOM` (`"oom"`) instead of the usual
exit or stop reason. Kills from before the session started are not
counted. Details in `docs/notes/59-oom-reason.md`.

## Preflight

`acphost.Preflight(spec)` checks the host prerequisites for the
isolation a spec asks for, without spawning or writing, so a bad host
config can be caught once at load time:

```go
r, err := acphost.Preflight(spec)
if err == nil && !r.OK() {
	for _, c := range r.Checks {
		if !c.OK {
			log.Printf("%s: %s (fix: %s)", c.Name, c.Reason, c.Hint)
		}
	}
}
```

Features: `cgroup` (writable parent under cgroupfs with the needed
controllers in `cgroup.subtree_control`), `user_namespace`
(newuidmap/newgidmap on PATH, ids covered by `/etc/subuid` and
`/etc/subgid`), `sandbox` (bwrap) and `network` (pasta, and nft when `sandbox.deny` is
set). Features the
spec does not ask for are absent. Details in `docs/notes/58-preflight.md`.

Each feature has several named checks (`cgroup.controllers`,
`user.subuid`, `user.newuidmap`, `network.pasta`, ...) and a failed one
has a `Hint` with the host config fix, such as the `Delegate=` line or
the `usermod --add-subuids` command. `r.String()` is the report text, one
line per check with a `fix:` line under each failure, so a runbook can
print it and stop. Names, hints and the text are in
`docs/notes/115-preflight-hints.md`.

## Isolation report

Once the agent starts, the host writes `isolation.json` in the session
dir: what it actually applied, not what the spec asked for. Read it
with `acphost.ReadIsolation(dir)`:

```go
iso, ok, err := acphost.ReadIsolation(dir)
// ok is false (err nil) when the file is absent: an older host.
if err == nil && ok && iso.Cgroup != nil {
	fmt.Println(iso.Cgroup.Dir, iso.Cgroup.Limits)
}
```

- `cgroup`: `dir` and the `limits` (`file`, `value`) written.
- `user`: `uid_map` and `gid_map` lines given to newuidmap/newgidmap.
- `network`: `mode` (`host`, `none`, `pasta`), the `deny` CIDRs loaded
  and `pasta_started`.
- `bwrap`: the full argv run, including any pasta prefix.
- `pdeath_helper`: with pasta, the helper argv run in front of pasta
  and bwrap, so the chain is `pdeath_helper` then `bwrap`.

Unused parts are omitted. A host with no isolation writes `{}`.

## Spawn failures

When the agent never runs (missing command, cgroup setup, newuidmap or
the userns helper, pasta, the deny rules, or a bwrap argument or mount
bwrap rejects), the host writes `exit.json` with `Exit.Reason` set to
`acphost.ReasonSpawnFailed` (`"spawn failed"`) and the setup error in
`Exit.Error` (`"error"`), then exits. `Main` returns a
`*acphost.SpawnError`, and so does `Connect` once that host is gone, so a
client polling for the socket stops at once:

```go
nc, rd, w, err := acphost.Connect(ctx, dir)
var se *acphost.SpawnError
if errors.As(err, &se) {
	return fmt.Errorf("session %s: %w", name, err) // do not retry
}
```

Sandboxed hosts tell a bwrap failure from an agent exit by bwrap's
`--json-status-fd` lines on stderr. Details in
`docs/notes/100-spawn-failure.md`.

## Journal redaction

`WithRedactor` rewrites each frame's message before the host writes it
to journal.jsonl, for example to strip tokens:

```go
acphost.Main(dir, acphost.WithRedactor(func(from string, msg []byte) []byte {
	return tokenRE.ReplaceAll(msg, []byte(`"[redacted]"`))
}))
```

Only the journal is redacted, and so replays after a reconnect. Live
frames and frames given to an Exporter are the originals. If the
redactor returns invalid JSON the msg is written as `null` and the host
logs a warning. Details in `docs/notes/66-redactor.md`.

## Journal compression and index

After a host has exited, `CompressJournal(dir, level)` replaces
`journal.ndjson` with a seekable `journal.ndjson.zst` (level 1-22, 0
means 3). The readers and `JournalTail` read either form, and use
`journal.idx`, a (seq, offset) record every 256 frames, to seek instead
of scanning. A host will not start on a compressed journal
(`ErrJournalCompressed`). Details in `docs/notes/74-journal-zstd-index.md`.

## Session names and socket path

`CheckSpawn` rejects a session name that is not one path element or
whose GC root link name is over 255 bytes. With `WithHostDir(dir)` it
also rejects a session dir whose `acp.sock` path is over
`MaxSocketPath` (107 bytes). `CheckSocketPath` and `SocketPath` are
exported for callers that pick dirs. Both are `PermanentError`s. Details
in `docs/notes/79-session-names.md`.

## Isolation end-to-end test

`TestIsolationTwoSubIDSessions` runs two sessions on separate
`AllocSubIDs` blocks and checks each can read its own 0700 worktree but
not the other's. It needs newuidmap/newgidmap and at least two subid
blocks for the current user, and skips with the reason otherwise:

```sh
go test -run TestIsolationTwoSubIDSessions -v .
```

CI runs it. Details in `docs/notes/67-isolation-e2e.md`.

## In-process test host

Package `acphosttest` runs a host in the test process, which skips
spawning a host binary (about 1s per host under -race):

```go
h := acphosttest.Start(t, dir, acphost.Spec{Session: "s1", Command: agent, Dir: dir})
nc, rd, w, err := acphost.Connect(ctx, dir)
// ...
ex, _ := h.Exit() // waits for the agent to exit
```

Start returns once acp.sock accepts. When the agent exits, Main returns
and `h.Done()` closes. At test end, cleanup sends the stop method, waits
for Main and removes host.pid. While Main runs its SIGTERM and SIGINT
handlers apply to the whole test process. See `docs/notes/114-acphosttest.md`.

## Release notes

v0.8.0 (2026-11-05): typed spawn failures and the pdeath helper in
isolation.json. `Protocol` stays 1. Details in
`docs/notes/106-release-v0.8.0.md`.

- `ReasonSpawnFailed`, `Exit.Error`, `SpawnError` (#100).
- `Isolation.PdeathHelper` (#101).

v0.7.0 (2026-11-05): journal compression with a seq index, and session
name and socket path checks. `Protocol` stays 1. Details in
`docs/notes/90-release-v0.7.0.md`.

- `CompressJournal`, `journal.idx`, `ErrJournalCompressed` (#74).
- `CheckSpawn` session name checks, `WithHostDir`, `CheckSocketPath`
  (#79).
- `Spec.Cgroup` placement test (#80).

v0.6.0 (2026-11-05): journal redaction and a fix for subordinate id
maps. `Protocol` stays 1. Details in `docs/notes/68-release-v0.6.0.md`.

- `WithRedactor` (#66).
- `Spec.User` mapped to subordinate ids now starts (#67).
- Isolation end-to-end test (#67).

v0.5.0 (2026-11-05): subid allocation, preflight checks and an OOM exit
reason. `Protocol` stays 1. Details in `docs/notes/60-release-v0.5.0.md`.

- `AllocSubIDs` per-session subid blocks (#57).
- `Preflight` (#58).
- `ReasonOOM` in `Exit.Reason` (#59).

v0.4.0 (2026-11-05): cgroup setup, usage stats and an isolation report.
`Protocol` stays 1. Details in `docs/notes/53-release-v0.4.0.md`.

- `SetupDelegation` (#47).
- `MethodCgroupStats` notification and `Exit.Cgroup` (#48).
- `isolation.json` and `ReadIsolation` (#49).

v0.3.0 (2026-11-05): optional isolation for the agent. `Protocol` stays
1. All new spec fields are optional, and leaving them unset keeps the
v0.2.0 behavior.

- `Sandbox.Network = "pasta"` with `Sandbox.Deny` CIDRs (#33).
- `Spec.User` runs the agent as another uid/gid in a user namespace
  (#29, #32).
- `Spec.Cgroup` with `CPUMax`, `MemoryMax`, `MemoryHigh`, `SwapMax` and
  `PidsMax`, `cgroup.kill` on stop, `oom_kill` in `exit.json`, and
  `DelegatedCgroup()` (#27, #28, #41).

Earlier releases: `docs/notes/9-release-v0.1.0.md`,
`docs/notes/16-release-v0.2.0.md`, `docs/notes/38-release-v0.3.0.md`,
`docs/notes/53-release-v0.4.0.md`, `docs/notes/60-release-v0.5.0.md`,
`docs/notes/68-release-v0.6.0.md`.

## Develop

PR titles follow [Conventional Commits](https://www.conventionalcommits.org)
(`feat:`, `fix:`, `docs:` and so on), checked by the `pr-title` job.
release-please keeps a release PR open from them; merging it tags the
next 0.x version.

```
go build ./... && go vet ./... && go test ./...
golangci-lint run ./...   # v2.15.0, config in .golangci.yml
```

The sandbox integration tests skip unless `bwrap` is on PATH and user
namespaces work. The pasta test also needs `pasta`, `nft` and `bash`.
