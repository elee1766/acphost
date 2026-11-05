# JournalTail reads from the end (#112)

2026-11-05

`JournalTail` scanned forward from the last index record whenever an
index existed, so a plain journal with a sparse or stale index could cost
a long scan. jobshop carried its own backward reader
(`pkg/control/journaltail.go`); this moves it here.

- Plain journal: read a 64 KiB block from EOF, double it until a full
  last line with a seq fits. A trailing half-written line is skipped. The
  index is neither read nor written, index or not.
- Compressed journal with a seek table: decompress the last block only
  (blocks hold whole lines), going back a block if it has no frame. No
  index needed.
- Compressed journal without a seek table: the old path, the index,
  rebuilt by one scan if missing.
- Return values are unchanged. jobshop can drop its copy.

BenchmarkJournalTail, ~100 MB journal, before -> after:
plain 32.5 -> 29.7 us/op, compressed 505 -> 269 us/op (1.29 MB -> 0.52 MB
allocated).
