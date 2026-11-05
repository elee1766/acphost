# 105: CI test time

2026-11-05

The go job took 1m30s-1m55s. Most of it was the final `go test -race
./...` (56s on run 42029573143), which also re-ran the sandbox, userns,
cgroup and matrix tests the steps before it had just run (cgroup step 27s).

Slowest tests before, local `go test -race -json`:

| test | before | after |
|---|---|---|
| TestCompressRoundTrip | 22.1s | 0.9s |
| TestBackpressureNoController | 14.4s | 0.5s |
| TestBackpressureStalledController | 6.0s | 0.5s |
| TestRedactorJournalAndReplay | 2.0s | ~0.1s |
| TestExitReasonLease | 1.25s | 0.1s |
| TestIndexRebuild | 1.2s | 1.3s |
| TestExporter | 1.1s | 0.3s |
| TestCompressCrash | 0.5s | 0.55s |
| TestTailNoIndex | 0.4s | 0.4s |
| TestCustomPrefix | 0.2s | 0.2s |
| package total | ~56s | 6.6s |

Changes:

- `maxBlock`, `queueBytes` and `queueFrames` are package vars, so the
  big-frame and backpressure tests lower them and cross the same bounds
  with ~1 MB instead of 12-32 MB of data under the race detector.
- The lease check ticks at lease/4 (10ms to 1s) instead of every second,
  so a short lease ends close to its length. Production leases (60s)
  still tick each second.
- The redactor test stops the host instead of waiting out a 2s agent
  sleep. The exporter agent lingers 0.3s (stats tick every 50ms).
- CI: the final `go test -race` skips the tests the isolation steps
  already ran, and the sandbox step gets `set -o pipefail` since the
  final run no longer backs it up.

No t.Parallel: the tests that dominated were data-bound, and the ones
that now set package vars must not run alongside others.

First CI run on the PR: 1m6s. The cgroup step took 25s, 21s of it the
pasta flavours of TestMatrixStopKillsGrandchild and
TestMatrixLeaseKillsGrandchild (~5s each: the stop path waiting out the
5s SIGTERM to SIGKILL grace). Running the flavours with t.Parallel
failed: hosts in one test process share the subreaper and signal
handling. Instead the grace is an internal option (`killGrace`, 5s by
default) and those two tests set 200ms. The SIGKILL path they check is
the same; the matrix itself is unchanged.

Result: go job 43s on PR #107 (run 42031328351), was 1m30s-1m55s.
