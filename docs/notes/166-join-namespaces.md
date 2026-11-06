# 166: join existing namespaces

2026-11-06

`Spec.Join` maps a namespace type to a file to join instead of creating
one. Decisions:

- Joining runs util-linux `nsenter` in front of the agent (or in front of
  bwrap/pasta), the way bwrap and pasta are external tools already. Go
  cannot setns user or mnt from a multithreaded process, and the
  re-exec helper (#157) would have to do it before the runtime starts
  threads, which needs cgo. nsenter joins user first and mnt last.
  No new Go dependency (`x/sys/unix` for the ioctls).
- With join.user, nsenter gets `--preserve-credentials`: the agent keeps
  whatever ids the namespace's map gives the host user.
- With join.pid nsenter forks and the agent is its child, so the host
  starts it under the pdeath helper (the host's Pdeathsig would only
  reach nsenter). bwrap's `--unshare-pid` is dropped.
- Join with `User` is refused: the userns helper's new namespace would be
  created before the join and has no caps over the joined ones. Use
  join.user with a namespace that already maps the ids. join.net with
  sandbox network none or pasta is refused.
- CheckSpawn checks each path with NS_GET_NSTYPE and, as non-root,
  that join.user is a user namespace we own (NS_GET_OWNER_UID) and not
  our own. Joining other types without join.user is refused with the
  owner uid in the message.
- `isolation.json` gains `join`: type to `{path, ns}`, ns being the
  `type:[inode]` the path resolved to at start.
- `Pin(types...)` keeps namespaces alive in a holder process (re-exec,
  argv[0] `acphost-pin`) instead of bind-mounting the ns file, since an
  unprivileged caller cannot bind-mount on the host. As non-root it adds
  a user namespace mapping 0 to the caller. `Close` ends it.

Not here: the CLI `join:` section, `--join` and `--pid` flags wait for
the CLI in #165. `JoinPlan` is the helper its `--print` will use.

Protocol is unchanged: new optional fields, and an old host ignoring
`join` would run unjoined, so callers must only set it on a host built
with this change (same as `user` and `cgroup` before).
