# README for proc, the CLI and the pkg/ layout (#195)

2026-11-06

README gained sections for what landed in v0.13 to v0.15 without one:

- a package table under Install, and "Upgrading to v0.15" (import paths
  from #164, alias removal from #160, `proc.MaybeChild`),
- "Overlays" under Sandbox (#163),
- "Joining namespaces" (`Spec.Join`, `Pin`, #166),
- "Running a command: proc" (`Start`, `StartDetached`, `Open`, #157-#159),
- "CLI" (`cmd/acphost`, #165).

The Go snippets are short. Each has a full version that compiles in an
example test, so `go vet` and `go test` keep them honest:
`ExampleStart` (runs, with output checked), `ExampleStartDetached` and
`ExamplePin` (compiled only, they need the re-exec helpers), and
`ExampleOverlay` (compiled only, it needs the layer dirs).

docs/notes/27-cgroup-spec.md said the cgroup spec was "not yet wired
into spawn"; that line now says spawn applies it (#80).
