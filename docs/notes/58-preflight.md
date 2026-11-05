# 58: Preflight

2026-11-05

`Preflight(Spec)` checks the host prerequisites for the isolation a spec
asks for, without spawning or writing. A caller (jobshop, at config load)
can quarantine a bad host config once instead of failing every spawn
through `CheckSpawn`.

One check per requested feature, each ok or missing with a reason:

- `cgroup`: parent is under the cgroupfs root, exists, is writable by us
  (delegated) and has the controllers the limits need in
  `cgroup.subtree_control`.
- `user_namespace`: newuidmap and newgidmap on PATH, and the mapped ids
  covered by /etc/subuid and /etc/subgid (same rules as spawn, `userMaps`).
- `sandbox`: bwrap (or `Sandbox.Bwrap`) found.
- `network`: pasta (or `Sandbox.Pasta`) found, only when Network is pasta.

Features not asked for are absent, so a spec with no isolation gives an
empty report. The error return is reserved for failures of the check
itself; missing prerequisites are reported, not returned. The cgroupfs
root, /etc and PATH are fields of an unexported `preflightEnv` so tests
use a temp tree.
