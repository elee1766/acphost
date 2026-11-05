# #68 Upgrading from v0.3 to v0.6

2026-11-05

What a v0.3.0 caller gains in v0.4.0 through v0.6.0, and what it must
change. Nothing: no exported name was removed or changed in signature
between v0.3.0 and v0.6.0, `Protocol` stays 1, and every new feature is
off until the caller asks for it. A v0.3.0 client and a v0.6.0 host (or
the reverse) work together.

## Gains

- `SetupDelegation(own, controllers)` (v0.4.0, #47): with a systemd
  `Delegate=yes` unit, moves the caller into `<own>/controller` and
  returns `<own>/sessions` for `Cgroup.Parent`. Callers that used
  `DelegatedCgroup()` as `Cgroup.Parent` directly hit the cgroup v2 "no
  internal processes" rule once controllers are enabled; use this instead.
- Cgroup usage stats (v0.4.0, #48): `MethodCgroupStats` notifications
  with `CgroupStats` while connected, and `Exit.Cgroup` in exit.json.
  Clients that do not handle the notification ignore it.
- `isolation.json` and `ReadIsolation` (v0.4.0, #49): what the host
  actually applied (cgroup limits, id maps, network, bwrap argv).
- `Preflight(Spec)` (v0.5.0, #58): check isolation prerequisites at
  config load without spawning.
- `ReasonOOM` (v0.5.0, #59): `Exit.Reason` is "oom" when the session
  cgroup OOM-killed the agent. Code that compared Reason to "agent
  exited" to detect a normal exit now sees "oom" for that case.
- `AllocSubIDs` and `Alloc` (v0.5.0, #57): a separate subid block per
  session, so sessions cannot read each other's files.
- `WithRedactor` (v0.6.0, #66): rewrite frames before they reach
  journal.jsonl. Live traffic and exporters keep the originals.

## Fixes

- v0.6.0 (#67): a `Spec.User` mapped to subordinate ids (as from
  `AllocSubIDs`) failed to start with `setresgid: operation not
  permitted`. Mapping to the caller's own ids was not affected.

## Steps

1. `go get github.com/elee1766/acphost@v0.6.0`.
2. Optional: call `Preflight` where specs are loaded, `SetupDelegation`
   at startup under systemd, `AllocSubIDs` per session with
   `Alloc.User` for `Spec.User`, and pass `WithRedactor` to `Main`.
3. If you match on `Exit.Reason`, handle `ReasonOOM`.
