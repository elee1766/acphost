# #81 Wall-clock sleeps in tests

2026-11-05

- TestFollowJournal slept 300ms to show a half-written frame is not
  reported. Now the complete and partial frames are on disk before the
  follower starts: getting seq 2 and then, only after the newline, seq 3
  shows the same thing with no sleep.
- Example kept two 10ms poll loops, each with a comment: one waits for
  acp.sock to appear, one waits for the journal tail. Neither has an event
  a client outside the host can wait on, and an example should not use
  test hooks. The Alive poll before Connect was redundant and is gone.
- No clock interface or testing/synctest: the host's waits are on real
  processes, sockets and files, which synctest cannot fake.
- `go test -count=1 ./...` on this host: before 8.98s, after 9.37s.
  That is noise: the package time is set by host and cgroup tests that run
  real agents. TestFollowJournal itself no longer has its 300ms floor.
