# 156: sandbox, cgroup, userns packages

2026-11-06. Step 1 of #155.

The isolation code moved out of the root package so a caller can run any
command with it (later `proc`, #158) without the ACP host:

- `sandbox/`: Sandbox, Mount, DNS, NetworkApplied, Args (was
  SandboxArgs), BuildArgs, CheckDNS (was CheckSandboxDNS), PastaPrefix.
- `cgroup/`: Cgroup, LimitApplied, Stats (was CgroupStats), Delegated
  (was DelegatedCgroup), SetupDelegation, Prepare, Remove, Kill,
  WaitEmpty, ReadStats, OOMKills, OOMReason, Controllers.
- `userns/`: User, Range, Alloc, ParseSubIDs, AllocSubIDs, ReadSubIDs,
  Maps, Check, CheckSpan.
- `internal/xerr` (PermanentError), `internal/trace` (test hook, PATH
  lookups) and `internal/hostenv` (subid paths, caller gid lookups that
  tests replace) are shared by root and the packages without making
  test seams public.

Root keeps aliases (aliases.go) for the old names, so jobshop builds
unchanged. They go one release after jobshop moves (#160). No wire
format change: isolation.json types are aliases with the same JSON.

Tests moved with the code. Tests that need Spec, CheckSpawn or a running
host stayed in root.

Preflight checks only the isolation settings, so PreflightIsolation
takes `Isolated{Sandbox, User, Cgroup}`; Preflight(Spec) stays as a
wrapper so callers do not break.
