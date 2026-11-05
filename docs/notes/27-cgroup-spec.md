# #27 Spec.Cgroup groundwork

2026-11-05

- `Spec.Cgroup` (nil = no cgroupfs access) holds a delegated parent and
  cpu/memory/pids limits. `CheckSpawn` validates values and that each
  limit's controller is in the parent's `cgroup.subtree_control`, with a
  `PermanentError` otherwise.
- `prepareCgroup` makes `<parent>/<session>` and writes only set limits.
  Session names must be one path element, so nothing lands outside parent.
- `DelegatedCgroup` reads the `0::` line of `/proc/self/cgroup`. Writable
  uses access(2) W_OK via `syscall`, so no new dependency.
- Not yet wired into spawn (follow-up under #26). Protocol stays 1: the
  field is omitempty and older hosts ignore it.
