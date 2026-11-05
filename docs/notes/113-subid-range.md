# Check the whole mapped subid range (#113)

2026-11-05

acphost checked only the one outside id it maps. jobshop reserves a block
per session (AllocSubIDs) and re-checked that the block lies inside one
/etc/subuid and one /etc/subgid entry (its checkSlotRange, see #57).

- `User.MapCount` (JSON `map_count`, omitted when 0) is the size of the
  outside block. Above 1, `userMaps` (spawn, CheckSpawn, Preflight)
  requires `[MapUID, MapUID+MapCount)` inside one subuid entry and the gid
  block inside one subgid entry, else a PermanentError naming the file,
  the range and the caller's entries.
- Adjacent entries are not joined: newuidmap checks each map line against
  a single entry, and AllocSubIDs never cuts a block across entries.
- Only the first id is still mapped. 0 or 1 keeps the old behaviour.
- `Alloc.User` sets MapCount to the block size.
- An older host ignores the new field (it is optional JSON), so no
  Protocol bump: it only loses the extra check.
