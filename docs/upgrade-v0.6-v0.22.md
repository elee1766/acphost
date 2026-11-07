# Upgrading from v0.6 to v0.22

For a caller on v0.6.0 moving to v0.22.0. Earlier steps:
`docs/notes/68-upgrade-v0.3-v0.6.md`.

`Protocol` is 1 in every release from v0.1.0 to v0.22.0. No change in
this range touched spec.json's required keys, hello/welcome, the
extension methods or the journal lines, so `Connect` and `ReadSpec` accept
any v0.6 to v0.22 client with any v0.6 to v0.22 host. The limits on a
mixed pair are on-disk files (the host lock, v0.20) and new optional
spec keys an older host ignores. Each section says which.

## Steps

1. `go get github.com/elee1766/acphost@v0.22.0`.
2. Rewrite import paths (v0.12).
3. Use `sandbox`, `cgroup` and `userns` for the isolation names (v0.14).
4. Call `acphost.MaybeChild()` (or `proc.MaybeChild()`) first in `main`
   and in each `TestMain` that starts a host (v0.11).
5. Replace `acphost.LockFile` and `acphost.PidFile` with `proc`, and
   stop any host started by a build before v0.17 first (v0.20).
6. If you match on `Exit.Reason` text, use `ReasonSpawnFailed` (v0.8).
7. Optional: `StartDetached` (v0.17), the systemd cgroup driver (v0.21)
   and `InScope` (v0.22).

## BREAKING changes

The CHANGELOG has four BREAKING entries between v0.7.0 and v0.22.0:
v0.11.0, v0.12.0, v0.14.0 and v0.20.0. Each has a section below. v0.21
and v0.22 add opt-in features, covered after them.

### v0.11.0: `proc.MaybeChild()` is required

CHANGELOG: "callers (jobshop main, TestMains using user or pasta) must
call proc.MaybeChild()."

| | |
|---|---|
| old | nothing: helpers ran from a package `init()`, picked by `_ACPHOST_*` env vars |
| new | `acphost.MaybeChild()` first in `main` and in `TestMain` |
| why | no `init()` side effects in importers, and argv[0] instead of an env var picks the helper (`docs/notes/157-maybechild.md`) |
| Protocol | 1, any mix. The helper is the host's own binary, so only the host side needs the call |

Old (v0.6):

```go
func main() {
	flag.Parse()
	// ...
	_ = acphost.Main(dir)
}
```

New:

```go
func main() {
	acphost.MaybeChild() // first, before flags
	flag.Parse()
	// ...
}

func TestMain(m *testing.M) {
	acphost.MaybeChild()
	os.Exit(m.Run())
}
```

`acphost.MaybeChild(opts...)` is `proc.MaybeChild()` plus the detached
host child (v0.17); its `opts` (`WithExporter`, `WithRedactor`) go to
that host's `Main`. A program that never calls `acphost.StartDetached`
can call `proc.MaybeChild()` instead.

Without the call, a spawn that needs a helper fails before it starts
anything with `proc.ErrNoMaybeChild` (`acphost.ErrNoMaybeChild` from
`StartDetached`). The helpers are needed for `Spec.User`, sandbox
network `pasta`, `Spec.Join` with `pid`, `Pin`, `WithDNSCheck`, the
systemd cgroup driver, `StartDetached` and `InScope`. A spec with none
of these runs without the call, but add it anyway: a later config change
should not fail at spawn.

### v0.12.0: import paths under `pkg/`

CHANGELOG: "import paths moved under pkg/ (github.com/elee1766/acphost
-> github.com/elee1766/acphost/pkg/acphost, github.com/elee1766/acphost/proc
-> .../pkg/proc, and so on)."

| | |
|---|---|
| old | `github.com/elee1766/acphost` |
| new | `github.com/elee1766/acphost/pkg/acphost` |
| why | one layout, libraries in `pkg/` and the CLI in `cmd/`; the package name stays `acphost` so only imports change (`docs/notes/164-pkg-layout.md`) |
| Protocol | 1, any mix. Go API only |

The module path is unchanged, so `go get github.com/elee1766/acphost`
still works. Old to new:

| old import | new import |
|---|---|
| `github.com/elee1766/acphost` | `github.com/elee1766/acphost/pkg/acphost` |
| `github.com/elee1766/acphost/acphosttest` | `github.com/elee1766/acphost/pkg/acphosttest` |
| `github.com/elee1766/acphost/cgroup` | `github.com/elee1766/acphost/pkg/cgroup` |
| `github.com/elee1766/acphost/proc` | `github.com/elee1766/acphost/pkg/proc` |
| `github.com/elee1766/acphost/sandbox` | `github.com/elee1766/acphost/pkg/sandbox` |
| `github.com/elee1766/acphost/userns` | `github.com/elee1766/acphost/pkg/userns` |

A v0.6 caller only has the first line: `acphosttest` arrived in v0.8.2,
and `cgroup`, `proc`, `sandbox` and `userns` in v0.11.0. `acphost.X`
references keep their name.

Old:

```go
import "github.com/elee1766/acphost"
```

New:

```go
import "github.com/elee1766/acphost/pkg/acphost"
```

### v0.14.0: root isolation aliases removed

CHANGELOG: "Go callers must use the sandbox, cgroup and userns
packages. The JSON wire format is unchanged (same types), so no
Protocol bump."

| | |
|---|---|
| old | `acphost.Sandbox`, `acphost.Cgroup`, `acphost.User` and the others below |
| new | the same types in `pkg/sandbox`, `pkg/cgroup`, `pkg/userns` |
| why | the isolation code moved out of the root package in v0.11 so `proc` can run any command with it; the aliases were a bridge for jobshop (`docs/notes/156-isolation-packages.md`, `docs/notes/160-remove-aliases.md`) |
| Protocol | 1, any mix. Same types, same JSON |

| v0.6 name | v0.22 name |
|---|---|
| `acphost.Sandbox`, `acphost.Mount` | `sandbox.Sandbox`, `sandbox.Mount` |
| `acphost.DNS` (v0.8.5 to v0.13) | `sandbox.DNS` |
| `acphost.SandboxArgs` | `sandbox.Args` |
| `acphost.CheckSandboxDNS` (v0.8.6 to v0.13) | `sandbox.CheckDNS` |
| `acphost.Cgroup` | `cgroup.Cgroup` |
| `acphost.CgroupStats` | `cgroup.Stats` |
| `acphost.DelegatedInfo` | `cgroup.DelegatedInfo` |
| `acphost.DelegatedCgroup` | `cgroup.Delegated` |
| `acphost.SetupDelegation` | `cgroup.SetupDelegation` |
| `acphost.ReasonOOM` | `cgroup.ReasonOOM` |
| `acphost.User`, `acphost.Range`, `acphost.Alloc` | `userns.User`, `userns.Range`, `userns.Alloc` |
| `acphost.ParseSubIDs`, `acphost.AllocSubIDs` | `userns.ParseSubIDs`, `userns.AllocSubIDs` |

`acphost.PermanentError`, `acphost.Permanentf` and `acphost.Permanent`
stay. `acphost.Isolation`, `CgroupApplied`, `UserApplied`,
`NetworkApplied` and `LimitApplied` stay as aliases of the `proc` types
(same JSON).

Old (v0.6):

```go
spec := acphost.Spec{
	Protocol: acphost.Protocol, Session: "s1", Command: "claude-code-acp",
	Dir: work, LeaseMS: 600_000,
	Sandbox: &acphost.Sandbox{RW: []acphost.Mount{{Src: work}}, Network: "none"},
	Cgroup:  &acphost.Cgroup{Parent: parent, MemoryMax: "2G"},
	User:    &acphost.User{UID: 1000, GID: 1000, MapUID: 100000, MapGID: 100000},
}
var subuids []acphost.Range // from acphost.ParseSubIDs
d, _ := acphost.DelegatedCgroup()
if ex, ok := acphost.ReadExit(dir); ok && ex.Reason == acphost.ReasonOOM { /* ... */ }
```

New:

```go
spec := acphost.Spec{
	Protocol: acphost.Protocol, Session: "s1", Command: "claude-code-acp",
	Dir: work, LeaseMS: 600_000,
	Sandbox: &sandbox.Sandbox{RW: []sandbox.Mount{{Src: work}}, Network: "none"},
	Cgroup:  &cgroup.Cgroup{Parent: parent, MemoryMax: "2G"},
	User:    &userns.User{UID: 1000, GID: 1000, MapUID: 100000, MapGID: 100000},
}
var subuids []userns.Range // from userns.ParseSubIDs
d, _ := cgroup.Delegated()
if ex, ok := acphost.ReadExit(dir); ok && ex.Reason == cgroup.ReasonOOM { /* ... */ }
```

### v0.20.0: `host.lock` and `host.pid` removed

CHANGELOG: "acphost.LockFile and acphost.PidFile are removed and a host
no longer writes host.lock or host.pid."

| | |
|---|---|
| old | `acphost.LockFile` (`host.lock`), `acphost.PidFile` (`host.pid`) |
| new | `proc.LockFile` (`supervisor.lock`), `proc.SupervisorPidFile` (`supervisor.pid`), or `proc.Open(dir)` |
| why | since v0.17 the host is its own `proc` supervisor and held both locks; two locks needed a release order (#216). One lock is simpler (`docs/notes/222-drop-legacy-lock.md`) |
| Protocol | stays 1, see the mixed pairs below |

Old (v0.6):

```go
alive := acphost.Alive(dir)
b, _ := os.ReadFile(filepath.Join(dir, acphost.PidFile))
pid, _ := strconv.Atoi(string(b))
_ = syscall.Kill(pid, syscall.SIGTERM)
// poll acphost.Alive(dir) until false
```

New:

```go
alive := acphost.Alive(dir) // now probes supervisor.lock
d, err := proc.Open(dir)    // reads supervisor.pid: the host's pid
if err == nil && d.Running() {
	_ = d.Stop() // SIGTERM; the host stops the agent
}
ex, err := d.Wait(ctx) // blocks on supervisor.lock, no polling
```

`Alive` keeps its signature. Its probe is now a shared flock on
`supervisor.lock`, so a waiter in `Detached.Wait` is not taken for a
host. `CompressJournal` takes `supervisor.lock` exclusively while it runs,
so it fails while a host runs, as before.

Why `Protocol` did not bump: a bump would make `Connect` and `ReadSpec`
refuse hosts from v0.17 to v0.19 that are still running, and those
already hold `supervisor.lock`, so v0.20 reads them correctly.

Mixed pairs:

| client | host | works |
|---|---|---|
| v0.20+ | v0.17+ | yes |
| v0.20+ | before v0.17 (v0.6 included) | no: the host holds only `host.lock`, so `Alive`, `proc.Open` and `StartDetached` see no host, and `StartDetached` would start a second one |
| before v0.17 | v0.20+ | no: the client reads only `host.lock` and sees no host |
| v0.17 to v0.19 | before v0.20 | yes |
| v0.17 to v0.19 | v0.20+ | partly: `proc.Open`, `Running`, `Stop`, `Wait` and the single-host guard (`supervisor.lock`) work, but that client's `acphost.Alive` reads `host.lock` and reports false |

`Connect` itself works in every pair (it dials `acp.sock`). What breaks
is liveness, stop and the single-host guard. Before deploying a v0.20+
client, stop hosts started by a build before v0.17: a session dir with
`host.lock`, no `supervisor.pid`, and `flock -n host.lock true` failing.
Stop those with the old build or SIGTERM the pid in `host.pid`.
Leftover `host.lock` and `host.pid` files are ignored and can be
deleted. A program that runs client and host from one binary (as
jobshop does) only meets this with hosts left running across the
upgrade.

## Opt-in changes in v0.21 and v0.22

Not BREAKING: existing specs and calls behave as before. Listed because
they change how a host should run under systemd, and because an older
host ignores the new spec key.

### v0.21.0: cgroup drivers

| | |
|---|---|
| old | `cgroup.Cgroup{Parent: "/sys/fs/cgroup/..."}`: mkdir under a delegated dir |
| new | `Driver: cgroup.DriverSystemd`, `Parent` a slice name: a transient scope per session owned by the systemd manager |
| why | under a restartable unit with `Delegate=yes`, session cgroups that outlive the controller made the next unit start fail with EBUSY (`docs/notes/226-systemd-cgroup-driver.md`) |
| Protocol | 1. `cgroup.driver` is a new optional spec key. A host before v0.21 ignores it, reads the slice name as a cgroupfs `Parent` and fails its absolute path check before the agent starts. Run v0.21+ hosts before writing `driver: systemd` |

Old, and still the default (`Driver` empty or `cgroup.DriverCgroupfs`):

```go
spec.Cgroup = &cgroup.Cgroup{Parent: sessionsParent, MemoryMax: "2G", PidsMax: 512}
```

New:

```go
spec.Cgroup = &cgroup.Cgroup{
	Driver: cgroup.DriverSystemd, Parent: "acphost.slice",
	MemoryMax: "2G", PidsMax: 512,
}
```

The systemd driver needs the manager's private socket
(`cgroup.ManagerSocket`), and a non-root host must run inside its user
manager's tree. `Preflight` checks both. `isolation.json` gains
`cgroup.driver` and `cgroup.unit`. Picking a driver: README "Choosing a
driver".

### v0.22.0: `InScope`

| | |
|---|---|
| old | `acphost.StartDetached(dir, env)` or `StartDetached(dir, env, acphost.InCgroup(cgdir))` (v0.19) |
| new | `acphost.StartDetached(dir, env, acphost.InScope(slice))` |
| why | `InCgroup` under a restartable unit puts the host inside the unit's cgroup, so the unit is not empty when it stops; a transient scope is outside it (`docs/notes/240-host-scope.md`) |
| Protocol | 1, any mix. Nothing on disk or on the wire changes; only the client side starts the scope |

```go
d, err := acphost.StartDetached(dir, nil, acphost.InScope("acphost.slice"))
```

`InScope` with `InCgroup` is an error. It needs the same manager as the
systemd driver and `acphost.MaybeChild()` in `main`.

## Other behavior changes since v0.6

Not marked BREAKING, but code written against v0.6 can see them:

- v0.7.0: `CheckSpawn` rejects a session name that is not one path
  element (`/`, NUL, `.`, `..`) or whose GC root link name is over 255
  bytes (`docs/notes/79-session-names.md`).
- v0.8.0: a setup failure (missing command, bwrap, pasta, the userns
  helper) sets `Exit.Reason` to `ReasonSpawnFailed` with the text in
  `Exit.Error`, and `Main` and `Connect` return `*SpawnError`. Code that
  matched the old free-form `"start failed: "` reason must switch
  (`docs/notes/100-spawn-failure.md`).
- v0.8.0: `Exit.Cgroup` is a `*cgroup.Stats` now (was
  `*CgroupStats`, the same type through v0.13).
- v0.20.0: `Alive` reads `supervisor.lock`, see above.

Optional spec keys added in this range: `sandbox.dns` and
`sandbox.resolv_conf` (v0.8.5), `sandbox.overlay` (v0.11.1), `join`
(v0.11.2), `cgroup.driver` (v0.21.0). A host older than the key
ignores it: the agent runs without that overlay, DNS setting or join,
and `driver: systemd` fails as above. Upgrade hosts before writing
specs that use them.

## Gains worth adopting

- `acphost.StartDetached` and `acphost.MaybeChild` (v0.17): start the
  host detached instead of your own `Setsid` spawn of a subcommand that
  calls `Main`; `proc.Open(dir)` finds it after a restart.
- `acphost.InCgroup` (v0.19) or `acphost.InScope` (v0.22): pick the
  host's cgroup.
- `CompressJournal` and `journal.idx` (v0.7), `JournalTail` from the
  end (v0.8.1), inotify follow (v0.8.8).
- `Preflight` fix hints, the nft check and `WithDNSCheck` (v0.8.4 to
  v0.8.7).
- `acphosttest.Start` and `StartDir` (v0.8.2, v0.8.5): an in-process
  host for tests.
- `pkg/proc`: run any command with the same isolation, attached
  (`proc.Start`) or detached (`proc.StartDetached`), and the `acphost`
  CLI (v0.13) to try a config by hand.
