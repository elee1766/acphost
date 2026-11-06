# 210: the ACP host on proc's detached supervisor

2026-11-06. Item 2 of #208.

Before: acphost had no way to start a host detached. Each caller
(jobshop `spawnHost`) ran its own binary with `Setsid` and a
`session-host` subcommand calling `Main`, and the host kept its own
lock, pid file and exit record, separate from `proc.StartDetached`'s.

Now:

- `proc` splits its supervisor into parts: `Detach` (re-exec this binary
  as argv[0] in its own session, config on fd 3, a ready line on fd 4),
  `DetachedChild` (the child side, returns `ready(pid, err)`),
  `Supervise` (flock on `supervisor.lock`, `supervisor.pid`, atomic
  `isolation.json` and `exit.json`) and `PrepareDetached` (refuse a held
  dir, remove the old records). `proc.StartDetached` is built on them,
  and its behavior is unchanged.
- `acphost.Main` calls `proc.Supervise` and writes its records through
  it, so the host process is its own supervisor: no extra process
  between caller and host. `proc.Open(dir)` works on any session dir
  (`Running`, `Stop`, `Wait`).
- `exit.json` keeps `acphost.Exit`'s JSON. Its keys are a superset of
  `proc.Exit`'s (`exit_code`, `signal`, `oom_kill`, `cgroup`), so
  `Detached.Wait` reads the agent's exit code, OOM kill and cgroup
  stats. A spawn failure also sets `setup_error`, so a proc reader sees
  it as one. It is now written atomically.
- `acphost.StartDetached(dir, env)` starts a host for the spec in dir
  through `proc.Detach`, stderr to `host.log`, and returns once the agent
  started (a hook in `Main`), or the spawn failure as a `*SpawnError`.
- `acphost.MaybeChild(opts...)` replaces `proc.MaybeChild()` in a
  program that uses `StartDetached`: it dispatches the host child
  (argv[0] `acphost-host`) and passes `opts` to its `Main`. Without it
  `StartDetached` returns `ErrNoMaybeChild` instead of re-running the
  program's own main.

Kept for compatibility: `host.lock` and `host.pid`. Clients from before
this change (jobshop's `Alive`, `hostPid`, `stopHost`) read only those,
and a new client must still see a running host started by an older
release. The host takes `host.lock` first, so two hosts on one dir still
fail as before. Dropping them is a later, breaking change.

Duplicate code removed: acphost's own atomic writer (`writeFileAtomic`)
and the handshake code that lived only inside the proc supervisor. Main
had no detach code to remove; that lived in the callers.

`Protocol` stays 1: spec.json, hello/welcome, the extension methods and
the journal are unchanged. exit.json only gains `setup_error` on a spawn
failure, which older readers ignore.

Follow-up: jobshop can replace its `Setsid` spawn of `session-host` with
`acphost.StartDetached` and `acphost.MaybeChild(hostspec.HostOptions...)`,
and `stopHost`'s polling with `proc.Open(dir).Wait`.

Tests: `TestDetachedHostSurvivesCaller` (a child process starts the host
and exits; the host answers `Connect`, and after the agent exits
`proc.Open(dir).Wait` returns its exit code), `TestDetachedHostStop`,
`TestDetachedHostSpawnFailure`, `TestDetachedHostNoMaybeChild`, and
`TestCgroupDetachedHostExitStats` (cgroup stats in exit.json, CI cgroup
step).
