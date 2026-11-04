# #16 Release v0.2.0

2026-11-04

Adds an optional bubblewrap sandbox for the agent. Contents:

- `Sandbox` and `Mount` types and the optional `Spec.Sandbox` field
  (#14). Nil keeps the v0.1.0 behavior.
- `SandboxArgs`, which builds the bwrap argv from a `Sandbox` (#14).
- The host runs the agent through bwrap when `Spec.Sandbox` is set, and
  `CheckSpawn` validates the sandbox as a permanent error (#15).
  `exit.json` reports the agent's exit code or signal, not bwrap's.

`Protocol` stays 1: the new spec field is optional and older hosts
ignore it. No breaking API changes.

Install: `go get github.com/elee1766/acphost@v0.2.0`
