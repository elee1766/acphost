# 251: godoc pass on pkg/...

2026-11-07. Part of target #246.

Read `go doc -all` for each package and fixed doc comments that were
stale or said nothing beyond the name. Comments only: no identifier,
signature or behavior changed.

Packages reviewed: `acphost`, `acphosttest`, `cgroup`, `hostenv`, `proc`,
`reexec`, `sandbox`, `trace`, `userns`, `xerr`.

Stale text fixed:

- `acphost.Main`: said it was the body of jobshop's `session-host`
  subcommand. It now says what it does (read the spec, take the
  supervisor lock, listen) and how `StartDetached` runs it.
- `acphost.Host`: said "runs one session" but has no exported API. It
  now points at `Main` and `StartDetached`.
- `acphost.Protocol`: now lists what it versions (spec.json, hello and
  welcome, extension methods, journal lines) and that a Go API change
  alone does not bump it.
- `MethodCgroupStats` and `statsLoop`: named `CgroupStats`, removed in
  v0.14 for `cgroup.Stats`.
- `Spec.Cgroup`: now mentions the systemd driver.
- `cgroup.Prepare`: says it is the cgroupfs driver whatever `Driver` is,
  and that `proc.Start` picks `StartScope` for systemd.
- `Pin`: named only `proc.MaybeChild`, now also `acphost.MaybeChild`.
- `aliases.go`: a note about aliases removed in #160 sat above
  `PermanentError`. Replaced with what `PermanentError` is.
- `proc`: package comment pointed at "#155". It now names `Start`,
  `StartDetached` and `Open`.

Package comments: every package already had one. Those that did not
name an entry point now do (`cgroup`, `sandbox`, `userns`, `proc`,
`acphost`, `acphosttest`, `xerr`, `trace`, `hostenv`), and `reexec` says
callers use `MaybeChild` in `proc` or `acphost` instead.

Type aliases `CgroupApplied`, `LimitApplied`, `UserApplied`,
`NetworkApplied` and `JoinApplied` said only "is proc.X". They now say
what the type is.

`grep -rn 'host.lock\|host.pid' pkg/` hits only tests that assert those
files are no longer written, or are ignored (`lock_test.go`), and one
false positive: `.` matches the space in "host pid" in a comment in
`pkg/reexec/userns.go`. No code uses the files. Pre-`pkg/` import paths
and the removed alias names appear in
no comment under `pkg/` or `cmd/`.

The revive `exported` and `package-comments` rules were already on in
`.golangci.yml`; no config change.
