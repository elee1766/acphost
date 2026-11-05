# #68 Release v0.6.0

2026-11-05

Changes since v0.5.0:

- `WithRedactor` and `Redactor`: rewrite each frame before it is
  journaled (#66, PR #70).
- Isolation end-to-end test `TestIsolationTwoSubIDSessions`: two
  `AllocSubIDs` sessions cannot read each other's 0700 worktree (#67,
  PR #69).
- Fix: the user namespace helper keeps CAP_SETUID/CAP_SETGID as ambient
  caps across its exec and clears them before running the agent, so
  `Spec.User` mapped to subordinate ids works (#67, PR #69).

Protocol stays 1: the journal format is unchanged and the rest is new
API or a fix. No breaking API changes.

Upgrading from v0.3: `docs/notes/68-upgrade-v0.3-v0.6.md`.

Install: `go get github.com/elee1766/acphost@v0.6.0`
