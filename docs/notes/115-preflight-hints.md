# Preflight fix hints (#115)

2026-11-05

jobshop's live isolation check (jobshop #489) checked delegated cgroup
controllers and /etc/subuid in shell because `Preflight` (#58) gave one
pass/fail per feature and no way to fix it. Now each prerequisite is its
own `PreflightCheck` with a `Name`, and a failed one carries a `Hint`:
the host config change that fixes it. A runbook prints
`report.String()` and stops on `!report.OK()`.

Checks, in report order (only for the features the spec asks for):

| Name | Fails when | Hint |
|---|---|---|
| `cgroup.parent` | parent relative, outside the cgroupfs root, missing or not a dir | where to put it; `SetupDelegation` |
| `cgroup.writable` | parent not writable by the host user | systemd `Delegate=yes` (or `systemd-run -p Delegate=yes`), or `chown -R <uid>` |
| `cgroup.controllers` | a controller the limits need is not in `cgroup.subtree_control` | not in `cgroup.controllers` either: add it to the unit's `Delegate=` line (user@.service drop-in); otherwise `echo "+memory" > .../cgroup.subtree_control` |
| `user.newuidmap`, `user.newgidmap` | not on PATH, or not setuid root and no file capabilities | install uidmap; `chown root` and `chmod u+s` |
| `user.subuid`, `user.subgid` | file malformed, or the mapped ids (`[MapUID, MapUID+MapCount)`) not inside one entry of the caller | `usermod --add-subuids <first>-<last> <user>` (same for gids) |
| `user.map` | the rest of the spawn checks (groups not held, a gid mapped twice) | fix `spec.user` |
| `sandbox.bwrap` | bwrap not found | install bubblewrap or set `sandbox.bwrap` |
| `network.pasta` | pasta not found (Network pasta only) | install passt or set `sandbox.pasta` |

`cgroup.writable` and `cgroup.controllers` are skipped when
`cgroup.parent` fails, and `user.map` when a subid check fails, so one
gap gives one failure.

Report text (`PreflightReport.String`), one line per check and an
indented fix line under each failure:

```
ok   cgroup.parent
ok   cgroup.writable
FAIL cgroup.controllers: controllers cpu not enabled in /sys/fs/cgroup/.../sessions/cgroup.subtree_control (cpu not in /sys/fs/cgroup/.../sessions/cgroup.controllers)
     fix: controllers cpu are not delegated to this cgroup: add them to the unit's Delegate= line ...
FAIL user.subuid: user: uids 165536..166535 are not inside one /etc/subuid entry (entries: 100000..165535)
     fix: as root: usermod --add-subuids 165536-166535 jobshop
```

The text is for people. Code should switch on `Name` and `OK`; reason
and hint wording may change.

`Feature` keeps its values, so a caller grouping by feature still works,
but a feature now has several checks instead of one. JSON gains `name`
and `hint`. No Protocol bump: the report is not on the client/host wire.

Tests build a fake cgroup root, etc and PATH in a temp dir and remove
one piece per case. The setuid check is a field of `preflightEnv` since a
test cannot make a root-owned setuid file.
