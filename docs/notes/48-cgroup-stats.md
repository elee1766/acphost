# #48 Session cgroup usage stats

2026-11-05

With `Spec.Cgroup`, the host reads `memory.current`, `memory.peak`,
`cpu.stat` usage_usec and the `oom_kill` and `max` lines of
`memory.events` into `CgroupStats`.

- Live: `statsLoop` sends `MethodCgroupStats` (`<prefix>cgroup_stats`) on
  each tick while a controller is connected. The values are the kernel's
  counters, not deltas, so nothing is kept between ticks and a tick with
  no controller is simply skipped.
- Final: read just before the cgroup is removed and stored in
  `Exit.Cgroup`. `Exit.OOMKill` now comes from the same read.
- A missing file reads as 0. `memory.peak` needs Linux 5.19.
- Protocol stays 1: older clients ignore an unknown notification and an
  unknown exit.json field.
