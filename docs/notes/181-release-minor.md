# Minor releases without release-as (#181)

2026-11-06

release-please-config.json had `bump-patch-for-minor-pre-major: true`, so
before v1 a feat commit bumped only the patch. Every minor release then
needed a PR setting `release-as` and a follow-up PR to drop it (#94,
#145/#148, #151/#152).

It is now false. With `bump-minor-pre-major: true` kept, the rule before
v1 is:

- feat: minor (v0.11.0 -> v0.12.0)
- fix, perf: patch (v0.11.0 -> v0.11.1)
- breaking (`!` or `BREAKING CHANGE:`): minor, as AGENTS.md asks

The release PR picks the version from the commits since the last tag,
so release-as is no longer needed for a minor. Use it only to force a
version the commits would not give.
