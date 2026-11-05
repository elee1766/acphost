# 59: Exit.Reason ReasonOOM

2026-11-05

With Spec.Cgroup, the host reads memory.events `oom_kill` when it
prepares the session cgroup and again just before removing it. If the
count rose, exit.json gets `reason: "oom"` (`ReasonOOM`) instead of
"agent exited" or the stop reason. Comparing against the start value
means a reused cgroup with earlier kills does not mislabel a clean exit.
A missing file reads as 0, so no cgroup or no memory controller keeps the
normal reason. `Exit.OOMKill` is unchanged.

Protocol stays 1: Reason is a free-form human-readable string that older
clients already pass through, so a new value breaks nothing.
