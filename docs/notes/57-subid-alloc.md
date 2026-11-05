# 57: AllocSubIDs, per-session subid blocks

2026-11-05

`AllocSubIDs(lockDir, uids, gids, size)` cuts the caller's subuid and
subgid ranges into whole blocks of `size` ids. Block i of the uid ranges
pairs with block i of the gid ranges. It takes the first block whose lock
file (`lockDir/subid-<uid>-<gid>-<size>.lock`) it can flock without
blocking, the same way the host lease works. flock is per open file, so
goroutines in one process exclude each other too, and the kernel drops the
lock when the holder dies, so a dead holder's block is free with no
cleanup. Lock files are never removed, so unlinking cannot race a new
holder.

No free block, a size of zero, or ranges too small is a `PermanentError`.
`Alloc.User(uid, gid)` gives a `User` mapped to the block's first ids.

Callers must use the same `size` for one lockDir: lock names include size,
so mixed sizes over the same range would not exclude each other.

Off by default: nothing calls it unless the caller does. No Protocol bump.
