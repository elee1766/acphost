# #88 Conventional commits and release-please

2026-11-05

- PR titles are checked by `pr-title` (action-semantic-pull-request v5)
  on `pull_request_target`, so it runs for fork PRs without checking out
  their code.
- release-please runs in manifest mode, `release-type: go`. The manifest
  starts at 0.6.0, the latest tag, so the next release continues from it.
  `bump-minor-pre-major` keeps breaking changes on 0.x minor bumps, as
  AGENTS.md asks, and features bump the patch.
- The release workflow uses a GitHub App token (`RELEASE_APP_ID`,
  `RELEASE_APP_KEY`), because PRs opened with GITHUB_TOKEN do not trigger
  CI. A release is cut only by merging the release PR.
- The hand-written release notes in README stay for 0.6.0 and earlier.
  From now on CHANGELOG.md has them.
