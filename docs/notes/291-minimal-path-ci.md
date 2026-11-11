# #291 CI job with isolation binaries absent from PATH

2026-11-11

#281 passed CI only because the runner had newuidmap; the dev worker
host, which has none, caught it. Target #284 wants `go test ./...` to
give the same verdict on any Linux host. The `minimal-path` job in
ci.yml checks that for the five binaries the isolation code looks up:
newuidmap, newgidmap, bwrap, pasta and nsenter.

How it works:

- The job installs the same packages as the `go` job (bubblewrap,
  passt, nftables, uidmap), so the binaries are on the runner. Hiding
  them, not leaving them out, is the point: a test that runs them by a
  fixed path instead of a PATH lookup still shows up.
- `$RUNNER_TEMP/minpath` holds symlinks to `go` and to the tools test
  scripts call: sh, cat, echo, env, grep, head, id, readlink, sed, seq,
  setsid, sleep, touch, tr, true. The step prints the dir and, for each
  of the five binaries, its runner path and that `command -v` fails on
  the minimal PATH. It fails the job if one is found.
- Tests run with `PATH=$MINPATH CGO_ENABLED=0 go test -json -count=1
  ./...`. With cgo on, `go test` needs gcc (runtime/cgo) and `-race`
  needs cgo, so this job runs without the race detector; the `go` job
  keeps `-race`. GOCACHE and GOMODCACHE are passed explicitly so they
  stay where setup-go put them.
- Other missing tools (systemd-run, nft, bash, getent) are absent too,
  and the tests that need them skip, which is the behavior wanted.

A test that needs a tool not in the list above fails with "executable
file not found". Either the test should skip naming the tool, or, if
the tool is basic (coreutils level) and needed by many tests, add it to
the list.

The job writes `test-minpath.json` but uploads no `failed-tests`
artifact: that name belongs to the `go` job, and `jobshop report
ci-failures` reads only it (see 263-failed-tests-artifact.md).

The first run on the PR was green, so `continue-on-error` was not
needed. The required check to add on master's branch protection is
`ci / minimal-path` (job `minimal-path` of workflow `ci`).
