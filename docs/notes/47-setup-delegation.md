# #47 SetupDelegation

2026-11-05

Cgroup v2 forbids processes in a cgroup whose children have controllers
enabled (no internal processes). A host started with `Delegate=yes` sits
in its own delegated cgroup, so it cannot enable controllers for session
cgroups there. `SetupDelegation(own, controllers)` fixes the layout:

- `<own>/controller`: the calling process moves here.
- `<own>/cgroup.subtree_control`: the controllers are enabled.
- `<own>/sessions`: created, controllers enabled, returned for
  `Cgroup.Parent`.

Each step checks first (dir exists, pid already in cgroup.procs,
controller already enabled), so a repeat call writes nothing. A
controller missing from `<own>/cgroup.controllers`, an unreadable
`cgroup.controllers`, or EACCES/EROFS on a mkdir or write is a
`PermanentError` naming the file. Other errors are plain so a caller may
retry. The pid is injectable internally so tests run on a temp dir.
