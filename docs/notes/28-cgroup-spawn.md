# #28 Agent in the session cgroup

2026-11-05

- `startAgent` runs `prepareCgroup` and starts the agent (or bwrap) with
  `UseCgroupFD`, so clone3 places it in the cgroup before it runs and no
  fork can escape. Setpgid and Pdeathsig stay.
- Stop: SIGTERM to the group as before, then after 5s `cgroup.kill`, or
  the group SIGKILL if the kernel has no `cgroup.kill`. After the agent
  exits the host kills the cgroup and polls `cgroup.events` for
  `populated 0` (5s bound) before writing exit.json.
- `Exit.OOMKill` (`oom_kill`, omitempty) is set from `memory.events`.
  Older readers ignore it. Protocol stays 1.
- The session cgroup is removed after exit and after a failed start.
- Integration tests skip without a writable delegated cgroup that has
  memory and pids in subtree_control. `ACPHOST_TEST_CGROUP` can name a
  prepared empty parent. They skip in CI and on the jobshop host (no
  delegation), so the kill and OOM paths have not run yet. Run them on
  a host with delegation before relying on them.
