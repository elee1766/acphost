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

Empty. The code is being extracted from
[jobshop](https://github.com/elee1766/jobshop) `pkg/host`, which uses it
to run agent sessions that survive controller restarts. See the issue
labeled `target`.

## Develop

```
go build ./... && go vet ./... && go test ./...
golangci-lint run ./...   # v2.15.0, config in .golangci.yml
```
