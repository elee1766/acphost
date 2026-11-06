# #162 Sandbox mounts as the caller, agent as the subuid

2026-11-06

Problem: with `User` and `Sandbox`, the userns helper switched to the
mapped subuid before it ran bwrap, so bwrap did every bind as that
subuid. A mount source under a 0700 dir of the caller (a home) failed.

Now, when `userns.RootMaps` applies (the agent is not root inside and
not mapped to the caller's uid):

- uid_map is `0 <caller uid> 1` plus `<UID> <MapUID> 1`. gid_map adds
  `0 <caller egid> 1` unless egid is already mapped. isolation.json shows
  every line.
- The helper stays ns root (`reexec.UsernsConfig.Root`: no ambient caps,
  no setresuid) and execs bwrap, which mounts with the caller's access.
- bwrap runs a bound copy of the host binary (`reexec.UsernsInit`,
  `/dev/.acphost-init`, in bwrap's own /dev tmpfs) with `--cap-drop ALL
  --cap-add CAP_SETUID --cap-add CAP_SETGID`. It reads the ids from
  `UsernsInitEnv` (pasta passes no fds past 2), sets groups, gid and uid,
  clears ambient caps and execs the agent: CapEff 0.
- bwrap makes missing mount-point parents 0700, owned by ns root, which
  the agent could not cross. `Sandbox.OpenParents` (set by the host only)
  adds `--perms 0755 --dir` for each parent in the sandbox's own root,
  never for a path inside a bind or overlay (a host dir).
- pasta gets `--netns-only`. Without it pasta makes its own user
  namespace mapping only root, so there is no agent uid to switch to.
  The net namespace is then owned by our namespace, where the agent has
  no caps, so it still cannot drop the deny rules. pasta keeps running
  its command and exiting with it, as before.

Tried and dropped: bwrap `--userns`/`--uid` needs bwrap to setuid in a
namespace it cannot set up from root here (it fails "unable to switch to
gid"). pasta attached to an existing netns by pid keeps running after
the namespace is gone.

Unchanged: no sandbox (helper switches ids itself), User mapped to the
caller's own uid (the agent is the caller, nothing to fix), and a joined
user namespace (`Join["user"]`).

Ubuntu 24.04's AppArmor profile `bwrap-userns-restrict` denies
capabilities to processes bwrap starts, so the init helper's setresgid
fails with EPERM even with CAP_SETGID in CapEff (the error now prints
ids, caps and maps). CI unloads that profile; a host using it needs the
same, or `MapUID` set to the caller's own uid.

Release note for jobshop: the pre-spawn check no longer needs to check
host-side parents of mount sources for sandboxed User sessions (JOB-53).
