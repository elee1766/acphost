# Detached exit.json carries OOM and cgroup stats (#201)

2026-11-06

Question: does a detached command's exit.json, read through `proc.Open`,
carry the same `OOMKill` and `Cgroup` stats as attached `Wait`?

Yes, by construction: the supervisor calls `Process.Wait` (which reads
the cgroup's final stats and compares oom_kill against the count at
prepare time) and marshals the whole `proc.Exit` into exit.json.
`Detached.Wait` and `Detached.Exit` unmarshal the same struct. No code
change was needed.

`TestCgroupDetachedOOM` (pkg/proc) pins it: a detached shell grows a
variable past a 32 MiB memory.max (swap capped at 0 where the kernel
has swap accounting, #41), and the test asserts `OOMKill`, signal
`killed`, positive `Cgroup.OOMKills` and `MemoryMaxEvents` from
`Open(dir).Wait`, and that the cgroup was removed. It skips without a
writable delegated cgroup with pids and memory, like the other cgroup
tests; CI's cgroup step runs it (the name matches `TestCgroup`).
