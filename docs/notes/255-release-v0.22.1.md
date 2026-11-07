# #255 Release v0.22.1

2026-11-07. Last item of target #246.

Changes since v0.22.0, all docs:

- Upgrade guide v0.6 to v0.22, `docs/upgrade-v0.6-v0.22.md`: old and new
  code, why, and the `Protocol` a mixed client/host pair needs for each
  breaking change from 0.11 to 0.20, plus the cgroup drivers (0.21) and
  `InScope` (0.22). README links it (#247, PR #250).
- Runnable examples for `pkg/cgroup`: cgroupfs (`Delegated`,
  `SetupDelegation`, `Prepare` with `ReadStats`, `Kill`, `WaitEmpty`,
  `Remove`) and systemd (`CheckManager`, `StartScope`, `StartHostScope`)
  (#248, PR #252).
- `ExampleInScope` and `ExampleInCgroup` in `pkg/acphost`, each saying
  when to choose it, linked from README (#249, PR #253).
- godoc pass on `pkg/...`: stale comments fixed (Main, Protocol,
  CgroupStats, the removed aliases), and every package comment names its
  entry point (#251, PR #254).

No API change. `Protocol` stays 1.

Release: master had only `docs:` commits since v0.22.0, which
release-please does not count as releasable, so it opened no release
PR. `release-as: 0.22.1` is set in release-please-config.json (as note
94 did), and the PR that adds this note has a `fix` title, since a
chore or docs commit alone does not trigger release-please (see note
126, #145). Remove `release-as` in a follow-up once v0.22.1 is tagged.

Install: `go get github.com/elee1766/acphost@v0.22.1`
