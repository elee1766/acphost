# #106 Release v0.8.0

2026-11-05

Changes since v0.7.0:

- Spawn setup failures are typed: `Exit.Reason` is `ReasonSpawnFailed`
  with the setup error in the new `Exit.Error` field, `Main` returns
  `*SpawnError`, and so does `Connect` once that host is gone. Failures
  inside bwrap, pasta or the userns/pdeath helpers are told apart from
  an agent that ran and failed (#100, PR #104). Details in
  `docs/notes/100-spawn-failure.md`.
- `Isolation.PdeathHelper` (`pdeath_helper` in isolation.json): the
  helper argv run in front of pasta and bwrap (#101, PR #102).
- CI go job under 60s: slow tests cut, sandbox tests not run twice
  (#105, PR #107). Details in `docs/notes/105-ci-test-time.md`.
- jobshop master now pins acphost v0.7.0 (#99, elee1766/jobshop#656).

Protocol stays 1. `error` and `pdeath_helper` are new optional fields
that older readers ignore. Callers matching the old free-form
`"start failed: "` reason should switch to `ReasonSpawnFailed`.

Release: release-please proposed 0.7.1 (`bump-patch-for-minor-pre-major`
makes feat commits a patch bump before v1), so `release-as: 0.8.0` is set in
release-please-config.json, as #94 did. Remove it once v0.8.0 is tagged.

Install: `go get github.com/elee1766/acphost@v0.8.0`
