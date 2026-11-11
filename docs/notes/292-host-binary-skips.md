# #292 tests with isolation binaries missing

2026-11-11

Target #284 item 3: fix the tests the minimal-PATH CI job (#291) shows
failing instead of skipping. That job was green from its first run:
with none of newuidmap, newgidmap, bwrap, pasta and nsenter on PATH,
each test skips at its first `need*` helper. A host with only some of
them is where failures show, so locally the same minimal PATH was run
with each binary added alone and in groups (newuidmap/newgidmap from
the host's setuid wrappers, pasta, bwrap, nsenter, nft, bash, getent).

Every set that held newuidmap and newgidmap but not bwrap failed:

- `TestUsernsFileOwnerAndExit/sandbox` and `TestUsernsReconnect/sandbox`:
  `usernsSpecs` captured the parent test's `t`, so the `needBwrap` skip
  inside the subtest ran `SkipNow` on the parent from the subtest's
  goroutine, which Go reports as "subtest may have called FailNow on a
  parent test". The builders now take the subtest's `t`, so the subtest
  skips with "bwrap not on PATH".
- `TestIsolationTwoSubIDSessions`: its agent scripts call `mkdir` and
  `rm`, which were not on the minimal PATH (exit 127). A host has them,
  so these are not missing-tool skips but a PATH list gap: `mkdir`,
  `rm` and `ls` (pkg/sandbox overlay test) join the list in ci.yml and
  the README.

No test hit a host preflight before a spec error, the #282 case, in
any set tried.

CI: the minimal-path job gains a second test step with only newuidmap
and newgidmap added to the minimal PATH, and the job adds the runner's
subid blocks so `TestIsolationTwoSubIDSessions` runs there instead of
skipping. The job never had `continue-on-error` (#291 was green), so
there is nothing to remove.

The minimal PATH dirs moved from `$RUNNER_TEMP` to `/tmp/minpath` and
`/tmp/minpath-userns` (mode 755). The first CI run of this PR failed
`TestIsolationTwoSubIDSessions` with `mkdir: Permission denied`: its
agents run as a subordinate uid, which cannot traverse the runner's
home to follow the symlinks. The README's local recipe chmods its
mktemp dir to 755 for the same reason.

Fixed tests, as failed before on the minimal PATH plus newuidmap and
newgidmap: `TestUsernsFileOwnerAndExit/sandbox`,
`TestUsernsReconnect/sandbox`, `TestIsolationTwoSubIDSessions`.
