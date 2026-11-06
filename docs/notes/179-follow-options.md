# Follow intervals as options (#179)

2026-11-06

FollowJournal's intervals (inotify safety poll, poll fallback, inotify
on/off) were package vars. Tests set them and reset them in t.Cleanup,
which raced with follower goroutines still reading them under -race.

They now live in an unexported `followOpts` passed per call to
`followJournal`. `FollowJournal` keeps its signature and passes
`defaultFollowOpts()` (5 s safety poll, 200 ms fallback, inotify on).
Tests build their own options, so no package state changes at test
time. No exported option was added: no caller has asked to tune these.
