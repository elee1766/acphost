# Journal redaction hook (#66)

2026-11-05

`WithRedactor(func(from string, msg []byte) []byte)` lets a caller rewrite
each frame's message before `Host.record` writes it to journal.jsonl, for
example to strip tokens a prompt or tool call carried.

- Only disk is redacted. Live frames forwarded between agent and controller,
  and frames given to an Exporter, are the originals. Replay after a
  reconnect reads the journal, so replayed frames are redacted.
- If the redactor returns invalid JSON, the msg is written as `null` and the
  host logs a warning. A broken line would make the journal unreadable for
  `ReadJournal` and replay.
- Default off: with no option the journal bytes are unchanged.
- No wire change, Protocol stays 1.
