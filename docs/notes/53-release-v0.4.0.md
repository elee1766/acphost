# #53 Release v0.4.0

2026-11-05

Changes since v0.3.0:

- `SetupDelegation` prepares a delegated cgroup for sessions (#47, PR #51).
- `MethodCgroupStats` notification with `CgroupStats`, and `Exit.Cgroup`
  in exit.json (#48, PR #50).
- `isolation.json`, the `Isolation` types and `ReadIsolation` (#49, PR #52).

Protocol stays 1. The exit.json field is optional, isolation.json is a
new file older clients do not read, and the notification is
`_`-prefixed, so older clients ignore it. No breaking API changes.

README gained sections for all three. After merge: tag `v0.4.0` on master.

Install: `go get github.com/elee1766/acphost@v0.4.0`
