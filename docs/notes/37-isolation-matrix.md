# #37 Combined isolation tests

2026-11-05

- No-fields guard: `trace.go` adds a package hook that the helper
  lookups (`lookPath`, `lookCommand`), the newuidmap/newgidmap runs,
  subuid/subgid reads and every cgroupfs entry point report to.
  `TestNoFieldsTouchesNothing` runs spawn, reconnect, stop and exit.json
  with no isolation fields and asserts none of them fired, with
  /etc/subuid pointed at a missing path.
- Matrix (`matrix_test.go`): {cgroup}, {user}, {pasta}, {all three} x
  {exit 3, Stop kills a grandchild, lease kills a grandchild, host
  SIGKILL takes the agent (Pdeathsig), reconnect}. With a cgroup the
  grandchild calls setsid so only cgroup.kill reaches it. Each feature
  skips on its own when the host lacks it.
- CI runs the matrix in the delegated cgroup step, so all four rows run
  there. On the jobshop host, user and pasta run and cgroup skips.
- The matrix found no bugs.
