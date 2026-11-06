# Pasta tests run five times in CI (#180)

2026-11-06

Pasta tests passed once on their PR and then failed on master (#131,
fixed twice in parallel by #132 and #133). One run on the PR was not
enough to show the flake.

ci.yml now has a "pasta tests x5" step after the sandbox tests:
`go test -race -count=5 -run 'TestSandboxPasta' ./...`. It writes
test-pasta.json like the other test steps, so a failure shows in full and
lands in the failed-tests artifact (#178). It runs even if the sandbox
step failed.

The pasta tests are short, so the extra runs cost a few seconds; the
measured go job time is in the PR.
