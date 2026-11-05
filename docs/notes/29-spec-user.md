# 29: Spec.User and id maps (2026-11-05)

Groundwork for #26. `Spec.User` names the uid/gid the agent runs as inside
a user namespace and the outside ids they map to. Nothing creates the
namespace yet.

- Ownership: map_uid must be the caller's euid or in its /etc/subuid
  range. map_gid and each group must be held by the caller (egid or
  getgroups) or in its /etc/subgid range. Groups map to themselves.
- Missing subuid/subgid files mean no ranges, not an error.
- `userMaps` sorts gids so the newgidmap arguments are deterministic.
- `CheckSpawn` only reads the files and looks up newuidmap/newgidmap when
  User is set. Protocol stays 1: the field is optional and omitted when nil.
