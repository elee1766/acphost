# CI failed-tests artifact (#178)

2026-11-06

Every go test step in ci.yml runs `go test -json` into `test-<step>.json`
and prints the output field back with jq, so the log shows the full,
unfiltered test output. The grep filter that used to sit there hid the
real TestSandboxPastaDNS error (#131). The step keeps the go test exit
code in `rc`, so a pipe never swallows a failure.

The cgroup and other test steps run even when an earlier test step
failed (`success() || failure()`), so one red step does not hide the
results of the rest.

On failure a step collects every `"Action":"fail"` event that has a Test
from all the JSON files into `failed-tests.json`, a list of
`{"package", "test"}`, and uploads it as the `failed-tests` artifact.
That is the format `jobshop report ci-failures` reads (jobshop
pkg/flaky). Unlike jobshop's CI there is no rerun step, so `verdict` is
left out. acphost does not import jobshop, and jq is on the runner.
