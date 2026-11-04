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

## Develop

```
go build ./... && go vet ./... && go test ./...
golangci-lint run ./...   # v2.15.0, config in .golangci.yml
```
