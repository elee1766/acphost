# 159: proc.StartDetached and proc.Open

2026-11-06. Step 4 of #155.

`proc.StartDetached(dir, spec, grace)` runs a command that outlives the
caller. It re-execs this binary as a supervisor (argv[0]
`acphost-supervise`, dispatched by `proc.MaybeChild`) in its own session
with no parent-death signal. The supervisor:

- takes an exclusive flock on `dir/supervisor.lock` for its lifetime and
  writes `supervisor.pid`,
- runs the command with `proc.Start` (same isolation, setup-error
  detection and cgroup cleanup as attached), stdout and stderr to
  `output.log`,
- writes `isolation.json` once started and `exit.json` (proc.Exit) when
  it ends, then releases the lock.

The config goes in as JSON on fd 3; the supervisor reports the started
pid or the Start error on fd 4, so a bad command fails `StartDetached`
itself. Both fds are set close-on-exec in the supervisor so the command
does not hold the status pipe open.

`proc.Open(dir)` reads the pid file; `Running` is a non-blocking flock
probe, `Wait` blocks on the flock (no polling) and returns `exit.json`,
`Stop` sends SIGTERM to the supervisor, which runs `Process.Stop(grace)`.
A second `StartDetached` on a running dir is refused.

The supervisor lives in `proc`, not `reexec`, because it calls `Start`.
`reexec.ReadConfig` is exported for it. The ACP host still runs its own
detached process (`acphost.Main`); moving it onto this is a separate
change.
