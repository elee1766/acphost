# 241: unit restart test

2026-11-07. Part of target #239.

The JOB-97 case (note 226) was checked only in jobshop: restarting the
unit that started session hosts must succeed, and the sessions must live
on with their limits. With cgroupfs and `Delegate=` the unit's cgroup has
to enable controllers for session cgroups below it, and the unit's next
start failed with EBUSY.

`TestSystemdUnitRestart` (`pkg/acphost/systemd_restart_test.go`) runs in
the CI step "systemd cgroup tests", which matches `TestSystemd`:

1. `systemd-run --user --unit=acphost-test-caller-*.service
   --property=Delegate=yes` runs the test binary itself as the caller
   (`ACPHOST_TEST_UNIT_CALLER`, dispatched in `TestMain`, so no helper
   binary needs building). The caller starts a detached host with
   `InScope(slice)` unless one is alive, then waits. The spec has
   `Driver: systemd` with `memory_max` and `pids_max`.
2. The test waits for the host to answer on `acp.sock` and checks that
   the host scope and the session scope are in the slice, outside the
   unit's cgroup, and the limit files.
3. `systemctl --user restart` the service. It must succeed, end `active`
   with `Result=success`, and have a new main pid.
4. Both scope dirs still exist with the same limit files, the host is
   alive and answers with the same pid and cgroup.
5. Stopping the host collects both scopes.

The caller is the same binary, started with `-test.run=^$`, so the
second start (after the restart) sees `Alive(dir)` and does not start a
second host, as jobshop adopts a live host.

Skips like the other `TestSystemd` tests without a user manager, and when
`systemd-run` or `systemctl` is missing; the CI step fails on any skip.
Run 10 times with `-race` locally and leaves no units behind.

No API or `Protocol` change.
