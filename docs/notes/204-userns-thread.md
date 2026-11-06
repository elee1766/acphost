# 204: userns helper execs on the thread it cleared caps on

2026-11-06

`TestUsernsOverlay` failed once in CI with bwrap's "Unexpected
capabilities but not setuid". In non-root mode the userns helper
starts with ambient CAP_SETUID/CAP_SETGID, switches ids, clears the
ambient set with prctl and execs bwrap. Go applies setresuid/setresgid
to every thread, but prctl(PR_CAP_AMBIENT_CLEAR_ALL) and
PR_SET_PDEATHSIG are per thread. If the goroutine moved to another
thread between the prctl and the exec, bwrap ran with caps and a
non-zero uid and refused to start.

`becomeUser` now calls `runtime.LockOSThread` first, so the prctls
and the exec happen on one thread. `pdeathChild` already does this.
