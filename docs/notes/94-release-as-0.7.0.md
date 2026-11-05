# 94: release 0.7.0 through release-please

2026-11-05

Since v0.6.0 master has only fix commits under conventional titles
(#88 started them), so release-please proposed 0.6.1. The changes listed
in docs/notes/90-release-v0.7.0.md add API (CompressJournal,
WithHostDir and others) and need a minor bump before v1.

`release-as: 0.7.0` in release-please-config.json makes the release PR
propose 0.7.0. A `Release-As:` commit footer would also work, but squash
merges can drop it. Remove `release-as` in a follow-up PR once v0.7.0 is
tagged, or every later release PR proposes 0.7.0 again.

The release job now appends a link to `docs/notes/*-release-<tag>.md`, if
one exists, to the GitHub release body that release-please writes. Do not
tag by hand: the manifest would no longer match the tags.
