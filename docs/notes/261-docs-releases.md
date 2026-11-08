# #261 docs commits release on their own

2026-11-08

release-please opens a release PR only when the commits since the last
release produce a non-empty changelog. It builds that changelog from
`changelog-sections`, and a commit whose type has no section, or a
`hidden: true` section, adds nothing. Its README says the same: `docs` is
releasable only where the strategy lists it (Java, Python). The `go`
strategy lists only feat, fix and perf. So a master with only `docs:`
commits got "No user facing commits found" and no release PR.

release-please-config.json now has a visible `docs` section,
"Documentation". A `docs:` commit lands in the changelog, so it opens a
release PR. The bump stays a patch, since only `feat` and breaking
changes bump more.

chore, ci, build, refactor, test and style still have no section and
still do not release.

This replaces the workaround in notes 94, 126 and 255: a `fix`-titled PR
with `release-as` (or a `Release-As:` footer), then a cleanup PR to drop
`release-as`. Keep `release-as` for a minor bump that the commit types do
not give on their own (note 94), and still remove it after the tag.
