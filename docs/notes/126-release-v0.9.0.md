# #126 Release v0.9.0

2026-11-06

Changes since v0.8.0 (v0.8.1 to v0.8.8 were patch releases of these):

- `JournalTail` reads from the end of the journal instead of scanning it
  all (#112). Journal follow uses inotify instead of 200ms polling (#137).
- Subuid/subgid checks cover the whole mapped range, not only its start
  (#113).
- Package `acphosttest`: an in-process test host, `Start` and `StartDir`
  for a dir whose spec is already written (#114, #127).
- `Preflight` reports each isolation host config gap with a fix hint
  (#115), checks nft when sandbox deny is set (#135), and has an opt-in
  DNS check, `WithDNSCheck` (#136).
- Sandbox pasta DNS forward and resolv_conf settings (#124).
- jobshop stops connect retries on `SpawnError` and uses `JournalTail`
  directly (elee1766/jobshop#721, #125).

Protocol stays 1. The new isolation fields are optional and older readers
ignore them.

Release: release-please would propose 0.8.9 (`bump-patch-for-minor-pre-major`),
so `release-as: 0.9.0` is set in release-please-config.json, as #106 did.
Remove it once v0.9.0 is tagged.

Install: `go get github.com/elee1766/acphost@v0.9.0`
