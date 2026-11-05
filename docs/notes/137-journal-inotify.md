# FollowJournal: inotify instead of 200 ms polling (#137)

2026-11-05

`FollowJournal` polled every 200 ms, reopening the journal each time.
jobshop keeps several followers per session (attach, chat, stall check),
so idle followers added up.

Now it watches the session directory with inotify (IN_MODIFY,
IN_CLOSE_WRITE, IN_CREATE, IN_MOVED_TO/FROM, IN_DELETE). Any event in
the directory wakes the follower, which rereads from its offset as
before. Watching the directory, not the file, covers the journal being
replaced or compressed to `.zst` (#74). The inotify fd is nonblocking
and wrapped in an `os.File`, so it parks on the runtime poller with no
thread held, and closing it ends the reader goroutine.

A 5 s safety poll stays so a missed event cannot stall a follower. If
inotify cannot be set up (no instances left, unsupported fs), it falls
back to the old 200 ms poll. API and output are unchanged.

No new dependency: inotify comes from golang.org/x/sys/unix, already
used (see 2-port.md), and builds with CGO_ENABLED=0.

## Benchmark

`go test -run '^$' -bench FollowIdle -benchtime 3x .`, 50 idle
followers for 1 s, then one appended frame (AMD EPYC 7413):

| mode    | CPU per second idle | latency of one frame |
|---------|---------------------|----------------------|
| poll    | 194 ms              | 95 ms                |
| inotify | 0.1 ms              | 0.3 ms               |
