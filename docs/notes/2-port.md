# #2 Port of jobshop pkg/host

2026-11-04

Copied from github.com/elee1766/jobshop `pkg/host` (host.go, journal.go,
queue.go, spawn.go, spec.go, gcroot.go, tests, testdata) into package
`acphost`. Changes vs jobshop:

- Package renamed `host` to `acphost`; tests import the new path.
- The `pkg/telemetry` import is gone. Conversation export is a hook:
  `Exporter` (Observe per frame, TakeCounts, Close) built by an
  `ExporterFactory` passed as `Main(dir, WithExporter(f))`. Nil or no
  option means no export. A factory error is logged and the session runs.
- `Spec.ConvExport` and the `ConvExport` type are removed. A caller that
  needs the export settings in the host passes them to its factory
  itself (for example through its own file or env). An old spec.json that
  still has `conv_export` parses fine, the field is ignored.
- `Main` takes variadic options. The stats period is an unexported
  option so tests can shorten it.
- New test `TestExporter` runs a host with a fake exporter.

Unchanged: spec, journal.ndjson and exit.json formats, hello/welcome
lines, `_jobshop/*` method names, GC root link name, `Protocol` = 1.
Making the method prefix and link name settable is #3.

Dependencies (same versions as jobshop):

- golang.org/x/sys: prctl subreaper, flock, process group kill.
- github.com/stretchr/testify: tests only, as in jobshop.
