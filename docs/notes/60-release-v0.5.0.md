# #60 Release v0.5.0

2026-11-05

Changes since v0.4.0:

- `AllocSubIDs` and `Alloc`: per-session uid/gid blocks inside the
  caller's subid range, held by flock (#57, PR #61).
- `Preflight(Spec)`, `PreflightReport`, `PreflightCheck`: check isolation
  prerequisites without spawning (#58, PR #62).
- `ReasonOOM`: `Exit.Reason` is "oom" when the session cgroup OOM-killed
  the agent (#59, PR #63).

Protocol stays 1. Reason was already a free-form string, and the other
changes are new API only. No breaking API changes.

Install: `go get github.com/elee1766/acphost@v0.5.0`
