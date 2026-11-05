# #32 Run the agent in a user namespace

2026-11-05

- With `Spec.User` set, the host re-execs its own binary
  (`/proc/self/exe`) with `CLONE_NEWUSER` and a sync pipe on fd 3. An
  `init` in the package sees `_ACPHOST_USERNS_CHILD`, waits for the host
  to run `newuidmap`/`newgidmap` on its pid, sets groups and ids, re-arms
  Pdeathsig (a credential change clears it) and execs the real argv. The
  pid the host waits on is therefore the agent, so Setpgid, stop and the
  exit code path are unchanged.
- Go's own `UidMappings` writes the maps directly, which an unprivileged
  host cannot do for more than its own id, so the setuid helpers are used.
- Sandbox: bwrap runs inside the namespace and creates a nested one
  (tested working). This replaces the `--userns <fd>` idea from the issue:
  same result, no fd plumbing, and bwrap's options stay as they are.
- Helper failures are `PermanentError` with stderr. With User unset
  nothing changes and no helper is looked up. Protocol stays 1.
- Integration tests map to the caller's own uid/gid (needs no subuid
  entry) and skip without the helpers or user namespace support.
