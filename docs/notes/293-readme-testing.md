# #293 README Testing section

2026-11-11

Target #284 item 4: pkg/acphost has about 30 `t.Skip` sites and the
README only said that sandbox tests need bwrap and the pasta test needs
pasta, nft and bash. The new "Testing" section in README.md replaces
that paragraph with a table: what each group of tests needs, which tests
those are, and the skip message they print without it.

How the table was built:

- Every `t.Skip`/`t.Skipf` in `pkg/acphost/*_test.go` (grep), grouped by
  helper: `needBwrap`, `needPasta`, `defaultGateway`, `fakeResolver`,
  `needUserns`, `subUser` and the two-block check, `needNsenter`/`pin`,
  `needCgroup` and the SetupDelegation move, `needSystemd` and the
  systemd-run/systemctl lookup, and the root checks.
- Tests per helper from the call sites, `matrixSpec` giving the
  `TestMatrix*` flavours.
- Messages checked against a run with the isolation binaries hidden from
  PATH (as the #291 CI job does), which printed each test's skip line.

Every skip message in pkg/acphost maps to a row, except
`helper for TestMatrixPdeathsig` (`TestMatrixHostHelper`, a subprocess
entry point that always skips), which the text below the table covers.
Skips in pkg/sandbox, pkg/proc and cmd/acphost get one sentence rather
than rows: they repeat the bwrap and delegated cgroup needs.

The section also has the commands for the full set (the steps of the
CI `go` job, as one script) and for the minimal-PATH run from #291.

Keep the table in step with the tests: a new `t.Skip` reason in
pkg/acphost needs a row, or a test added to an existing row.
