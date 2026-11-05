# 67: isolation end-to-end test

2026-11-05

`TestIsolationTwoSubIDSessions` (isolation_e2e_test.go) takes two
one-id blocks with `AllocSubIDs`, runs a session per block as inside
1000:1000, has each agent `mkdir -m 700` its own worktree in a shared
1777 dir (so the worktree is owned by the block's outside uid without
chown), then checks each agent reads its own secret and gets
"Permission denied" on the other's. A last pair of sessions removes the
worktrees, since the test user cannot.

It skips (with the reason) when newuidmap/newgidmap are missing, user
namespaces do not work, the caller has fewer than two subid blocks, or
`Preflight` fails for a block. The CI runner has uidmap and subids,
so it runs there.

Running it found that the userns helper could not setresgid to a
subordinate id: the caps CLONE_NEWUSER grants are dropped at the
helper's exec, before the maps exist. The helper now gets
CAP_SETUID/CAP_SETGID as ambient caps and clears them before exec'ing
the agent.

Run on a host with subids for the current user:

    go test -run TestIsolationTwoSubIDSessions -v .

The test adds o+x to the temp dir's parents below `os.TempDir()` so the
subordinate uids can reach the shared dir.
