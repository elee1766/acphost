# acphost

Go library for running an [ACP](https://agentclientprotocol.com) agent
(for example `jcode acp`, `claude-code-acp`, `gemini --experimental-acp`)
in a detached host process that outlives its client.

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
go get github.com/elee1766/acphost@v0.3.0
```

## Quick start

This needs none of the isolation features described below.

The runnable version is `Example` in `example_test.go`.

```go
// Client: write the spec into the session directory.
acphost.WriteSpec(dir, acphost.Spec{
	Protocol: acphost.Protocol, Session: "demo",
	Command: "claude-code-acp", Dir: workDir, LeaseMS: 600_000,
})

// Start the host detached: your binary, a subcommand whose body is
//     acphost.Main(dir)
// started with Setsid so it outlives the client.

// Connect. The welcome has the last journal seq and the cached
// initialize result. Then speak ACP on nc.
nc, rd, welcome, err := acphost.Connect(ctx, dir)

// After a client restart: send Hello{Since: &seq} instead to get the
// agent notifications journaled after seq replayed before live traffic.
// acphost.ReadJournal reads the journal directly.

// Stop the host: send the MethodStop notification on the socket.
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

## Release notes

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
`docs/notes/16-release-v0.2.0.md`.

## Develop

```
go build ./... && go vet ./... && go test ./...
golangci-lint run ./...   # v2.15.0, config in .golangci.yml
```

The sandbox integration tests skip unless `bwrap` is on PATH and user
namespaces work. The pasta test also needs `pasta`, `nft` and `bash`.
