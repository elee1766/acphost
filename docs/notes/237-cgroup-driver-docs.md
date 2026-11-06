# 237: README on choosing the cgroup driver

2026-11-06. Target #234 item 4.

The README described each driver but not when to pick which. jobshop
JOB-97 hit the case the systemd driver exists for, and the next caller
should not have to read note 226 to find it.

Change: README "Cgroup / Choosing a driver": a table by how the host
runs, the EBUSY reason to use `systemd` under a restartable unit, when
`cgroupfs` is enough (no manager, or an owned delegated subtree), what
`systemd` needs (manager socket, non-root host in the user manager's
tree) and a link to `docs/notes/226-systemd-cgroup-driver.md`. Also adds
`driver` to the listed JSON keys.

Docs only. No API or `Protocol` change.
