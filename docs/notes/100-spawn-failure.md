# 100: spawn setup failures as a typed error

2026-11-05

jobshop#359: with a bad bwrap or pasta setup, TestSandboxedSession sat
in a 30s Eventually waiting for a socket. The host did exit, but the
client could not tell why and kept polling.

Two kinds of failure:

- Before the agent process starts (missing command, cgroup prepare,
  subid files, newuidmap). `startAgent` returns an error. These already
  wrote exit.json, with a free-form `"start failed: ..."` reason.
- After it starts, inside bwrap, pasta or the userns helper. The host
  only saw a non-zero exit and recorded `"agent exited"`.

Now both write `Reason: ReasonSpawnFailed` with the text in the new
`Exit.Error` field, and `Main` returns `*SpawnError`.

The second kind is told apart from an agent that ran and failed by:

- bwrap `--json-status-fd 3`. bwrap writes `{"exit-code": n}` only after
  its command exited, so a non-zero exit without that line is a setup
  failure. fd 3 is a dup of stderr made by a `sh -c 'exec "$@" 3>&2'`
  right in front of bwrap: pasta closes extra inherited fds, and bwrap
  closes the status fd in the sandbox, so fd 2 itself would cut off the
  agent's stderr. isolation.json's `bwrap` argv shows the wrapper.
  The host reads the sandbox's stderr through a pipe, copies it to
  host.log, and keeps the last plain line as the error.
- The userns and pdeath helpers prefix their errors with
  `acphost userns: ` / `acphost pdeath: `; such a line plus a non-zero
  exit is a setup failure. Without a sandbox nothing else on stderr is
  read as one, since an unsandboxed agent's own stderr looks the same.

Client side: `Connect` fails to dial (socket gone) or gets EOF before
the welcome (the socket is up before the agent starts). In both cases
it checks exit.json; if it says spawn failed, is not older than
spec.json, and no host holds the lock, it returns `*SpawnError`. A
client that rewrites spec.json before each start never sees a stale
one.

`Protocol` stays 1: `error` is a new optional field, and the old reason
text was never a stable value. Callers matching `"start failed: "`
should switch to `ReasonSpawnFailed`.
