# 247: upgrade guide v0.6 to v0.22

2026-11-07. Part of target #246.

`docs/notes/68-upgrade-v0.3-v0.6.md` stopped at v0.6, and v0.6 to v0.22
had four BREAKING releases in two days. The guide is
`docs/upgrade-v0.6-v0.22.md`, outside `docs/notes/` because it is for
callers, not a design record.

How it was built:

- Every BREAKING line in CHANGELOG.md from 0.7.0 to 0.22.0 (0.11.0,
  0.12.0, 0.14.0, 0.20.0) has a section with the CHANGELOG text, an
  old/new/why/Protocol table, and old and new code.
- v0.21 (cgroup drivers) and v0.22 (`InScope`) are not BREAKING in the
  CHANGELOG. They get sections because they change how a host should run
  under systemd and because a pre-v0.21 host ignores `cgroup.driver`.
- Renamed and removed names were found by diffing `go doc -short` of
  v0.6.0, v0.10.0 and master, and the v0.13.0 `aliases.go`. Identifiers
  in snippets were checked with `go doc` on master.
- The mixed client/host table for v0.20 was checked against the v0.19.1
  source: a v0.17 to v0.19 client's `Alive` reads only `host.lock`, so it
  reports a v0.20 host as gone while `proc.Open` still works.

`Protocol` is 1 from v0.1.0 to v0.22.0, so the guide states once that
`Connect` and `ReadSpec` accept any pair, and per change what else a
mixed pair needs.

README: the "Upgrading to v0.15" section named the wrong releases (pkg/
layout v0.13, alias removal v0.15; they were v0.12 and v0.14). It is now
"Upgrading", a short list linking the guide.

Docs only. No API or `Protocol` change.
