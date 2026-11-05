# #114 acphosttest

2026-11-05

- jobshop tests ran `acphost.Main(dir)` in a goroutine and hand-rolled
  stop and host.pid cleanup to save about 1s per spawn under -race. Package
  `acphosttest` makes that supported: `Start(t, dir, spec, opts...)` writes
  the spec, runs Main, returns once acp.sock accepts, and on t.Cleanup sends
  the stop method, waits for Main and removes host.pid.
- Main left one goroutine per run: the signal watcher blocked on a channel
  that never fired. It now also returns on stop, and `signal.Stop` runs
  when Main returns, so in-process hosts leak nothing.
- Leak check without goleak: the tests compare `runtime.NumGoroutine`
  before and after, dumping stacks on failure. No new dependency.
- Exit semantics are in the package doc: agent exit ends Main and closes
  Done; test end stops the host; Main's signal handlers are process-wide
  while it runs.
