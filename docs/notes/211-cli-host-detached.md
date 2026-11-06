# 211: CLI detached mode for host configs

2026-11-06. Item 3 of #208. Needs #210 (note 210-host-on-proc.md).

```
acphost start --host -c FILE --dir SESSIONDIR [--lease 30m] [flags] -- CMD ARGS...
acphost status|wait --dir SESSIONDIR
acphost stop --dir SESSIONDIR [--grace 30s]
```

`start --host` is `host` made detached. It builds the spec the same way
(`--dir` is the session dir, the session is named after its base name,
the agent runs in the cwd), writes `spec.json`, and calls
`acphost.StartDetached`. It prints the host pid on stdout and the socket
path on stderr, and returns once the agent started.

- `status`, `stop` and `wait` are unchanged: the host holds the proc
  supervisor's lock and pid file (#210), so `proc.Open(dir)` works on a
  session dir. `wait` maps the exit record like `run` (128+n for a
  signal, 125 for a setup error). `stop` signals the host, which stops
  the agent (`Spec` kill grace, 5s) and writes `exit.json`; `--grace`
  only bounds how long the CLI waits before it SIGKILLs the host.
- `--grace` and `-c` follow `start`'s existing meaning. The agent's
  SIGTERM to SIGKILL delay in a host is fixed (5s); `--grace` is not
  passed to it. `--lease` is the host's lease, default 30m as for `host`.
- A running host in `--dir` is refused with exit 125 before `spec.json`
  is rewritten. A spawn failure (missing command, bad mount) is 125 with
  the setup error, as `host` reports it. `--host` with `run` or `host` is
  a usage error (2).
- The binary's `main` calls `acphost.MaybeChild()` now, so it can run the
  detached host child.

Tests: the host survives the CLI process (a built binary starts it and
exits, then `status` sees it, `Connect` succeeds, `wait` returns the
agent's exit code), `stop`, a second start on a running dir, and a spawn
failure.
