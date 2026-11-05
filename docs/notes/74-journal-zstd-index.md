# Journal compression and seq index (#74)

2026-11-05

Finished journals are large (36.5 MB to 1.75 MB at zstd -3 on gasserver)
and reading a tail or a seq scanned the whole file.

- `CompressJournal(dir, level)` writes `journal.ndjson.zst`, syncs it,
  writes `journal.idx`, renames into place, then removes
  `journal.ndjson`. It takes the host lock, so it fails while a host runs.
  The caller decides when to call it. Levels 1-22 map to
  klauspost/compress encoder levels: 1-2 fastest, 3-5 default, 6-9 better,
  10-22 best. 0 means 3.
- The .zst is one zstd frame per block (256 frames or 4 MB of lines,
  whichever first) and a seek table in a skippable frame at the end, laid
  out as in the zstd seekable format. A seek decompresses one block. Plain
  `zstd -d` reads it.
- `journal.idx`: 16-byte little-endian records (seq, offset into the
  uncompressed journal). The host appends one every 256 frames and one for
  the last frame at exit. Readers rebuild a missing or broken index with
  one scan and extend one that is behind the file (crash), and write it
  back unless a host holds the lock (the host owns the file then).
- Readers (`ReadJournal`, `ReadFrom`, `FollowJournal`, `JournalTail`)
  read the plain file if it exists, else the .zst. Both existing means a
  crash after the rename; the next `CompressJournal` removes the plain
  file. A stale `.zst.tmp` is removed by the next `CompressJournal`.
- `JournalTail` with no index on a plain file reads backwards from EOF
  and does not create an index.
- `Main` returns `ErrJournalCompressed` for a session whose journal is
  compressed: a compressed journal is final.
- Offsets returned by readers are uncompressed offsets, same as before for
  plain files. No wire change, Protocol stays 1.

Dependency: github.com/klauspost/compress, pure Go zstd, so
`CGO_ENABLED=0` builds keep working. The standard library has no zstd
encoder.
