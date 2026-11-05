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
go get github.com/elee1766/acphost@v0.2.0
```

## Usage

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

### Network

`Network` is `host` (default, the host's network), `none` (no network,
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

## Develop

```
go build ./... && go vet ./... && go test ./...
golangci-lint run ./...   # v2.15.0, config in .golangci.yml
```

The sandbox integration tests skip unless `bwrap` is on PATH and user
namespaces work. The pasta test also needs `pasta`, `nft` and `bash`.
