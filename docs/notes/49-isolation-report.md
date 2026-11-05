# #49 Effective isolation report

2026-11-05

Once the agent has started, the host writes `isolation.json` (an
`Isolation`) in the session dir: the cgroup dir and the limit files and
values written, the uid_map and gid_map lines given to
newuidmap/newgidmap, the sandbox network mode with the masked deny CIDRs
and whether pasta started, and the full argv (pasta prefix plus bwrap).

- Values come from the code that applies them: `prepareCgroup` returns
  the limits it wrote, `usernsCommand` the map lines, the internal
  `sandboxArgs` the network part next to the argv. Nothing is recomputed
  from the spec.
- Each part is nil when unused. A host with no isolation fields still
  writes `{}`, so "none applied" differs from "file absent" (older host,
  `ReadIsolation` returns false).
- `pasta_started` is set once the agent process starts under pasta. pasta
  only runs its command after configuring the namespace; a later pasta
  failure shows as the agent's exit.
- Written atomically (temp file plus rename).
- Protocol stays 1: a new file older clients do not read.
