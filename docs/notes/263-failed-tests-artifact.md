# #263 failed-tests artifact and jobshop report ci-failures

2026-11-08

`jobshop report ci-failures --repo elee1766/acphost` printed "no valid
artifacts found to download" for runs 42120892363 and 41992761473 (both
2026-11-05). Those runs come before #186 (2026-11-06), which added the
upload, so they have no artifact. The message is gh's stderr from `gh run
download`. jobshop goes on and lists those runs under `no_artifact`.
Run 42195836213 (2026-11-06, after #186) has the artifact, and jobshop
reads it: it reports `TestDetachedHostSurvivesCaller` in
`pkg/acphost`. The message goes away once the pre-#186 runs drop out of
the `--limit` window. Neither side needs a change.

The contract, as ci.yml writes it:

- Artifact name `failed-tests`, a single file `failed-tests.json`,
  default retention (90 days).
- A JSON array of `{"package": "<import path>", "test": "<name>"}`,
  one per failed test, unique. A failed subtest gives two entries, the
  parent `TestX` and `TestX/sub`, since go test fails both.
- Built from every `test-<step>.json` (`go test -json`) after all test
  steps. Each test step has `if: success() || failure()` and keeps the
  go test exit code in `rc`, so a failure in an early step still lets
  the later steps write their JSON.
- The list and upload steps run on `if: failure()`. A run red because of
  build, vet or a test step without a failed test (a package that does
  not compile, a TestMain exit) uploads `[]`. Lint runs after the
  upload, so a lint-only failure uploads no artifact, and jobshop lists
  that run under `no_artifact`.

Keep this shape when you change the test steps: jobshop parses
`package` and `test`, nothing else. A new test step must write
`test-<step>.json` to be included.

Checked on a throwaway PR with a deliberately failing test; the gh api
output is in the PR for #263.
