# 216: host.lock is released before supervisor.lock

2026-11-06

`TestDetachedHostSurvivesCaller` failed on master CI: `Alive(dir)` was
still true after `proc.Open(dir).Wait` returned.

Cause: `Main` took `host.lock`, then `proc.Supervise` (supervisor.lock,
#210), each with its own `defer`. Defers run last in, first out, so
supervisor.lock was released first. `Wait` blocks on supervisor.lock and
returns right after, while the host still held host.lock for a moment, and
`Alive` reads host.lock.

Fix: one defer releases host.lock, then supervisor.lock. A waiter that
returns from `Wait` then sees `Alive` false. The order is the one the
host's callers can rely on: supervisor.lock (what `Wait` and `Running`
read) is the last to go, and `exit.json` is written before either.

Reproduced on the old code under load (`go test -race -count=100 -cpu 1,4`
in four parallel runs: failures in three of four). With the fix, the same
stress has no failures. Plain `-count=50` did not reproduce it, because
the window is a few microseconds.
