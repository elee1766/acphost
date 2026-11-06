# 222: drop host.lock and host.pid

2026-11-06. Item 3 of #219.

Before: a host held two locks, `host.lock` (kept for clients from
before #210) and proc's `supervisor.lock`, and wrote both `host.pid` and
`supervisor.pid`. Two locks meant a release order to get right (#216).

Now `supervisor.lock` and `supervisor.pid` are the only ones:

- `Main` takes the dir with `proc.Supervise` alone. A second host on the
  dir fails there ("locked by another supervisor").
- `Alive(dir)` is proc's `Running` on the dir: a non-blocking shared
  flock probe of `supervisor.lock`. The old probe was exclusive. With a
  shared probe, a `proc.Detached.Wait` waiter (which holds a shared lock
  for a moment after the host exits) does not count as a live host.
- `CompressJournal` takes `supervisor.lock` exclusively (no pid file)
  while it runs, so a host cannot start under it. It fails if a host or
  a waiter in `Wait` holds the lock at that moment, as it did with a host.
- The index writer's "host running" check uses `Alive`.
- `acphost.LockFile` and `acphost.PidFile` are removed. Use
  `proc.LockFile` and `proc.SupervisorPidFile`, or `proc.Open(dir)`
  (`Pid`, `Running`, `Stop`, `Wait`).

## Protocol

`Protocol` stays 1. It versions spec.json, hello/welcome, the extension
methods and the journal, and none of them change. A bump would make
`Connect` and `ReadSpec` refuse hosts from v0.17 to v0.19 that are still
running. Those hosts already hold `supervisor.lock`, so this release
reads them correctly. Removing the constants breaks the Go API, so this
is a minor release (v0.20.0 at the earliest).

## Migration

Who breaks: only a host started by a build on acphost before v0.17. That
host writes only `host.lock` and `host.pid`, so this release's `Alive`,
`proc.Open` and `StartDetached` do not see it. `StartDetached` on that dir
would then start a second host while the old one still runs.

1. Before deploying a client on this release, check that no pre-v0.17
   host runs: a session dir with `host.lock` and no `supervisor.pid`
   whose `host.lock` is flocked (`flock -n host.lock true` fails). Stop
   those with the old build, or SIGTERM the pid in `host.pid`.
2. jobshop: remove the legacy fallback in `pkg/control/hostproc.go`
   (`legacyHostAlive`, `legacyHostPid`, `stopLegacyHost`, which use
   `host.Alive` on host.lock and `host.PidFile`) in the same PR that bumps
   to this release. It no longer compiles against it.
3. Leftover `host.lock` and `host.pid` files in old session dirs are
   ignored. They can be deleted.

A client from before v0.17 with a host from this release sees no host
(it reads only `host.lock`). jobshop runs client and host from one
binary, so that mix does not occur there.

## Tests

`TestMainLockIsSupervisorLock` (Main writes supervisor.pid and no
host.lock/host.pid, a second Main and CompressJournal fail while it runs,
Alive and Running go false after exit), `TestAliveReadsSupervisorLock`
(supervisor.lock decides, a held host.lock or a shared waiter does not),
`TestCompressJournalLock`, and the existing detached host tests.
