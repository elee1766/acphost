# #41 Cgroup.SwapMax (2026-11-05)

On a host with swap, an agent at `memory.max` is reclaimed into swap
instead of being OOM killed: a GitHub runner with 3 GiB swap showed 7814
`max` events and no `oom_kill`. Callers setting `memory_max` expect a hard
cap.

- `Cgroup.SwapMax` (json `swap_max`, byte count or `max`) is written to
  `memory.swap.max` in the session cgroup. It needs the memory controller
  and is validated like `memory_max`.
- Unset keeps the kernel default (no swap limit). acphost does not default
  it to 0: that would change what an existing spec does, and policy is the
  caller's job. Set `"0"` with `memory_max` for a hard cap.
- `TestCgroupOOM` now sets `SwapMax: "0"` instead of writing
  `memory.swap.max` on the test parent.
