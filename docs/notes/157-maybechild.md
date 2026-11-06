# 157: helper re-exec with proc.MaybeChild

2026-11-06

The userns, pdeath and sandbox DNS-check helpers re-exec the calling
binary. They used to be selected by `_ACPHOST_*` env vars checked in
package `init()`, so every importer ran the check and an env var was the
trigger. Now (moby `reexec` pattern):

- No `init()` anywhere in acphost (a test walks the tree). Programs call
  `proc.MaybeChild()` first in `main`, tests in `TestMain`.
- Dispatch is on argv[0]: `acphost-userns`, `acphost-pdeath`. pasta execs
  its command by path, so the DNS check is `<exe> acphost-dnscheck <ns>
  <name>` and dispatches on argv[1]; it only does a lookup.
- Input is JSON on an inherited fd: userns on fd 3 (uid, gid, groups,
  parent pid, exec path), written only after newuidmap/newgidmap, so the
  write is also the sync. pdeath on fd 4 (parent pid). Fixed and distinct
  so the userns helper can exec the pdeath helper with fd 4 in place.
- Fail closed: missing or malformed config, or a parent pid that is not
  the parent, exits 127.
- When a helper is needed and `MaybeChild` was not called, the spawn
  fails with `proc.ErrNoMaybeChild` before starting anything (an
  in-process flag; the re-exec runs the same binary, so the flag holds).

Code lives in `internal/reexec` because the `sandbox` package needs the
DNS helper and `proc` will import `sandbox` (#158). `proc` exports
`MaybeChild` and `ErrNoMaybeChild`.

Callers: jobshop's `main` and any `TestMain` that starts a host with
`user` or `network: pasta` must add `proc.MaybeChild()`. isolation.json
is unchanged (`pdeath_helper` is still `["/proc/self/exe"]`).

The pdeath helper is now started with Pdeathsig SIGUSR1, not SIGKILL.
The old env-var helper ran from `init()`, early enough that its prctl
landed on the main thread and replaced the host's SIGKILL. From `main`
the goroutine may be on another thread (the setting is per thread), so
the SIGKILL stayed on the main thread and killed the helper on host death
before it could kill pasta. TestMatrixPdeathsig/pasta caught it.
