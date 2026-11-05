# #90 Release v0.7.0

2026-11-05

Changes since v0.6.0:

- `CompressJournal(dir, level)`: seekable `journal.ndjson.zst` for a
  finished session, a `journal.idx` (seq, offset) index the host now
  writes, and `ErrJournalCompressed` from `Main` (#74, PR #75). Adds the
  dependency github.com/klauspost/compress (pure Go zstd).
- `CheckSpawn` rejects session names that are not one path element or
  whose GC root link is over 255 bytes. `WithHostDir`, `CheckSocketPath`,
  `SocketPath` and `MaxSocketPath` check the 107-byte unix socket path
  limit before the host starts (#79, PR #83).
- Test that the agent runs in `<parent>/<session>` with limits set, and
  note 27 updated: `Spec.Cgroup` was already wired (#80, PR #82).
- Tests: the 300ms sleep in TestFollowJournal is gone (#81, PR #84).
- README intro, badges, compiling quick start, package doc (#85, PR #87).
- CI pinned to ubuntu-24.04 (#77, PR #78).

Not in this release: the pasta flake (#76) is still open.

Protocol stays 1. The journal lines are unchanged. `journal.idx` and the
`.zst` are new files that older readers ignore, and an older host only
fails on a session whose journal was compressed, which is final anyway.
`CheckSpawn` gained a variadic option, so existing callers build. New
rejections of bad session names are the only behavior change for
callers.

Install: `go get github.com/elee1766/acphost@v0.7.0`
