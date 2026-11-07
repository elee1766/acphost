# 240: start a detached host in a transient systemd scope

2026-11-07. Part of target #239.

Problem: `InCgroup(dir)` (note 223) puts the detached host in an
existing cgroupfs dir. Under a restartable systemd unit that dir sits
inside the caller's unit, the tree note 226 moved agents out of: the
host, and an agent without `Spec.Cgroup`, stay in the unit's cgroup, so
the unit is not empty when it stops and its next start can meet the
same EBUSY.

Change: `acphost.InScope(slice)`, a `DetachOption`. `StartDetached`
passes `proc.DetachSpec.Scope{Slice, Name: spec.Session}` and `Detach`:

1. starts a holder (the `acphost-pin` re-exec, as the systemd driver
   does),
2. calls `cgroup.StartHostScope`: `StartTransientUnit` for
   `acphost-host-<session>-<holder pid>.scope` with `PIDs=[holder]`,
   `Slice=<slice>` (empty: the manager's default), accounting on,
   `OOMPolicy=continue`, `CollectMode=inactive-or-failed`. No limits,
   no `Delegate`.
3. clone3s the host into the scope dir (`UseCgroupFD`), then ends the
   holder.

The host runs in the scope dir itself, not in a leaf: the scope has no
controllers enabled for children and no limits, so there is no inner
node rule to satisfy. An agent with `Spec.Cgroup` still gets its own
session cgroup or scope from the driver; one without inherits the host's
scope. systemd collects the scope once the host and what it forked are
gone. There is no stop call.

`StartScope` and `StartHostScope` share `startUnit` (dial, start job,
wait, control group lookup). The holder start is shared in `proc`.

`InScope` with `InCgroup` is a permanent error, as is a slice name
systemd would refuse; both fail before anything starts or is written.
A scope start that fails (no manager, a non-root caller outside its
manager's tree) is `StartDetached`'s error before the host starts.

`Protocol` is unchanged: nothing on disk or on the wire changes.

Tests: `TestSystemdDetachedHostInScope` (CI step "systemd cgroup
tests"): host pid's cgroup is `<slice>/acphost-host-*.scope`, the agent
is in the same scope, and the scope is gone after the host exits.
`TestDetachedHostInScopeOptions` (any host): the exclusive and bad
slice errors. Unit tests for the unit name and properties in
`pkg/cgroup`.
