# 248: runnable examples for pkg/cgroup

2026-11-07. Part of target #246.

`pkg/acphost`, `pkg/proc` and `pkg/sandbox` had `Example*` tests and
`pkg/cgroup` had none, though it now has two drivers. `pkg/cgroup/example_test.go`
adds, in package `cgroup_test` (the caller's view):

- cgroupfs: `ExampleDelegated`, `ExampleSetupDelegation`, `ExamplePrepare`
  (which also shows `ReadStats`, `Kill`, `WaitEmpty` and `Remove` in
  teardown order).
- systemd: `ExampleCheckManager`, `ExampleStartScope`,
  `ExampleStartHostScope`.

None has an `// Output:` line. `go test` compiles an example without one
and does not run it, so CI needs no cgroup delegation or systemd user
manager, and the examples cannot go stale without failing to build.
pkg.go.dev renders them under each function. `go doc` does not print
examples (it skips `_test.go` files), so they do not show in
`go doc -all ./pkg/cgroup`, and `go test -list` omits examples without
an `Output:` line. `go vet ./pkg/cgroup` and `go test ./pkg/cgroup`
compile them.

The systemd examples start a `sleep` process to hold the scope, as
`proc.Start` does with its `acphost-pin` holder, because the manager
needs a pid to adopt.

Docs only. No API or `Protocol` change.
