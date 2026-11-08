# 262: TestDetachedHostSurvivesCaller flake

2026-11-08. Item 2 of #260.

Master run 42195836213 (2026-11-06, commit 80f9f04, #214) failed at
`detach_test.go:64`, `assert.False(t, Alive(dir))` right after
`proc.Open(dir).Wait` returned.

Cause: at 80f9f04 the host held two locks. `Wait` blocks on
`supervisor.lock`, `Alive` read `host.lock`. `Main` released them in two
defers, last in first out, so `supervisor.lock` went first and a caller
returning from `Wait` could still see `host.lock` held for a few
microseconds. This is the race in #216.

Fixed already on master, twice:

- #217 (3a1bfc5) released `host.lock` before `supervisor.lock`.
- #231 (12488d5, #222) removed `host.lock`. `Alive` is now proc's
  `Running`, a shared non-blocking probe of `supervisor.lock`, the same
  lock `Wait` waits on. With one lock there is no release order left, and
  the shared probe does not mistake a `Wait` waiter for a host.

The failing run was the only one: every master CI run since 3a1bfc5
passed.

Check, same binary and flags on both commits (`go test -race -c`, then 4
parallel `-test.count=100 -test.cpu 1,4`, 800 runs each):

- 80f9f04: 11 failures at detach_test.go:64.
- master 28299dd: 0 failures.
- `go test -race -count=50 -run TestDetachedHostSurvivesCaller
  ./pkg/acphost` on master: ok.

No code change. The test keeps its strict check, with a comment saying why
it needs no retry, so a second lock added later shows up here again.
