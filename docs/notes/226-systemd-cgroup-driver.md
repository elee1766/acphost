# 226: systemd cgroup driver

2026-11-06. jobshop JOB-97.

Problem: with cgroupfs and `Delegate=`, jobshop's unit cgroup must enable
cpu/memory/pids for session cgroups that outlive the controller. systemd
then cannot clone3 the next controller into the unit (EBUSY,
Result=resources). Turning the controllers off on stop resets live
sessions' limits to `max`. systemd v261's fix (#41304) cannot clear a
unit whose child still enables them.

Change: `cgroup.Cgroup.Driver`, `cgroupfs` (the default, unchanged) or
`systemd`. With systemd, `Parent` is a slice name and the session
cgroup is a transient scope the manager owns, so the caller's unit
enables nothing.

How a session starts (`proc.Start`):

1. Start a holder: the `acphost-pin` re-exec, which waits on stdin and
   dies with the host.
2. `StartTransientUnit` on the manager's private socket
   (`/run/systemd/private` as root, else `$XDG_RUNTIME_DIR/systemd/private`):
   `acphost-<session>-<holder pid>.scope` with `PIDs=[holder]`,
   `Slice=<Parent>`, `Delegate=yes`, accounting on, `OOMPolicy=continue`,
   `CollectMode=inactive-or-failed`, and the limits (`MemoryMax`,
   `MemoryHigh`, `MemorySwapMax`, `TasksMax`, `CPUQuotaPerSecUSec` and
   `CPUQuotaPeriodUSec` from `cpu_max`). Wait for the job's
   `JobRemoved`.
3. `mkdir <scope>/cmd` and clone3 the agent into it.

Why a holder and not the agent's pid: the issue proposed spawning the
agent, then adopting its pid. Whatever the agent forks before the scope
exists stays outside the limits, and with pasta or the userns helper the
started pid is not the agent. With the holder the scope exists first and
the agent is placed by clone3, as with cgroupfs. The agent goes in a leaf
because the scope holds the holder and has the limits.

Limits, stats and OOM come from the scope dir (the leaf is inside it),
read before the scope goes away: `cgroup.ReadStats`, `memory.events`
oom_kill, as before. `isolation.json` records the driver, the unit and
the limit files as the kernel has them (systemd rounds the CPU quota).

Stop and exit: `cgroup.kill` on the leaf, remove it, close the holder's
stdin. The scope is then empty and systemd collects it. There is no
`StopUnit` call. `OOMPolicy=continue` keeps an OOM kill to one process,
as with cgroupfs (systemd's default `stop` would kill the scope).

Checked first on systemd 260 with a user manager:
- The manager adopts a pid of the same uid that sits in another unit's
  cgroup (jobshop.service) into a new scope.
- A non-root caller must be in its manager's cgroup tree. Moving a
  process needs write access to the common ancestor's `cgroup.procs`.
- An empty scope's cgroup is removed at once, so stats are read before
  the holder exits.
- `AddRef` is refused on the private socket (it needs a bus name), and
  it is not needed.

Preflight: with systemd it checks the slice name (`cgroup.slice`) and
that the manager answers (`cgroup.systemd`) instead of the cgroupfs
parent checks.

Dependency: `github.com/godbus/dbus/v5`, the D-Bus client podman, runc
and the kubelet use. Pure Go (CGO_ENABLED=0 builds). Its only dependency
is `golang.org/x/sys`, which acphost already has. Talking to systemd
without D-Bus would mean shelling out to `systemd-run`, which cannot
adopt an existing pid with `--scope`, or writing the D-Bus wire protocol
by hand.

`Protocol` is unchanged: `spec.json` gains an optional
`cgroup.driver`, and older hosts ignore it. A spec with
`driver: systemd` sent to a host older than this release would be read
as cgroupfs with a slice name as `Parent` and fail its absolute path
check before anything starts.

Tests: unit tests for the unit name, properties, slice check and job
wait. `TestSystemdAgentInScope` (agent in `<slice>/<scope>/cmd`, limits
in the kernel files, scope collected) and `TestSystemdOOM` (OOM kill
reported) run in the CI step "systemd cgroup tests" under
`systemd-run --user --scope` with linger enabled. That step fails if the
tests skip. `TestNoFieldsTouchesNothing` also checks that no systemd
call happens without `Spec.Cgroup`.
