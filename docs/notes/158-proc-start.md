# 158: proc.Start, host startAgent uses it

2026-11-06. Step 3 of #155.

`proc.Start(ctx, proc.Spec, opts...)` runs a command attached to the
caller with the same isolation the host gives agents (sandbox, cgroup,
user namespace, pdeath helper with pasta). `Host.startAgent` is now a
thin wrapper around it.

- Moved into `proc`: the stderr tap and bwrap status-fd wrap (setup
  error detection), the userns spawn, `sandboxWithEnv`, and the
  isolation.json types. Root keeps `Isolation`, `CgroupApplied`,
  `UserApplied`, `NetworkApplied`, `LimitApplied` as aliases with the
  same JSON, and keeps `ReadIsolation` and the file write.
- `Process.Wait` does what the host did after the agent exited: SIGKILL
  the group, kill and drain the cgroup, read its stats, remove it, and
  turn bwrap's 128+n into a signal. It reports `Exit.SetupError` only for
  an exit nobody asked for (no Stop, no ctx cancel); the host maps it to
  `ReasonSpawnFailed` only when it did not stop the agent itself, as
  before. OOM: `Exit.OOMKill` compares against the count at prepare
  time; the host's `Exit.OOMKill` keeps its old meaning (any oom_kill).
- ctx done kills the cgroup (or group). The host passes no ctx and uses
  `Process.Stop(killGrace)`.
- `Spec.Name` is the cgroup name; the host passes the session name.
- Join (#166) moved with the spawn: `proc.Spec.Join`, the nsenter
  prefix, join.pid under the pdeath helper and `Isolation.Join` live in
  proc. The namespace checks stay in root `CheckSpawn`.
- Stdio defaults to /dev/null; the host passes pipes and its stderr.

No wire change. Detached start and `Open` are step 4 (#159).
