# 223: start a detached host in a given cgroup

2026-11-06. Prerequisite of #221 (jobshop on `acphost.StartDetached`).

jobshop starts each session host in its "sessions" cgroup (`UseCgroupFD`
on its own `Setsid` spawn, jobshop JOB-91), so the sessions CPU cap and
weight cover the host's load, not the controller's share.
`StartDetached` had no way to pick a cgroup, so switching to it would
have put hosts back in the controller's cgroup. Moving the host after
start is too late: `StartDetached` returns once the agent started, and
an agent without `Spec.Cgroup` has already inherited the caller's
cgroup.

Change:

- `proc.DetachSpec.CgroupDir`: when set, `Detach` opens the dir and
  starts the child with `UseCgroupFD`, so clone3 places it before it
  runs, as `proc.Start` does for `Spec.Cgroup`. The dir is opened before
  anything is spawned; a dir that cannot be opened is `Detach`'s error.
- `acphost.StartDetached(dir, env, opts ...DetachOption)` with
  `InCgroup(dir)`. The variadic parameter keeps existing calls
  compiling.

The cgroup must exist and the caller needs write access to its
`cgroup.procs` (cgroup v2 delegation rules). acphost does not create or
remove it: it belongs to the caller, and many hosts share it. This is
not `Spec.Cgroup`, which makes a per-session cgroup for the agent with
limits. Both can be used together: the host sits in the `InCgroup`
cgroup and the agent in `<Spec.Cgroup.Parent>/<session>`.

`Protocol` is unchanged: nothing on disk or on the wire changes.

Tests: `TestCgroupDetachedHostInCgroup` (CI cgroup step: host pid and
agent both in the given cgroup), `TestDetachedHostInCgroupMissing` (a
missing dir fails before the host starts, no `supervisor.pid` or
`exit.json`).
