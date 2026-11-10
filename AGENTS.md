# acphost

Go library: a detached host process for one ACP agent session (spawn,
socket proxy, journal, replay, lease). Go 1.27, module
`github.com/gfx-labs/acphost`. Origin: `pkg/host` in
github.com/gfx-labs/jobshop, its main user.

- Check: `go build ./... && go vet ./... && go test ./... && golangci-lint run ./...`
  (golangci-lint v2.15.0, config `.golangci.yml`).
- This is a library. No dependency on jobshop or on any one agent or
  telemetry backend. Callers plug in behavior (for example conversation
  export) through interfaces or callbacks.
- Keep the dependency list short. Record why each dependency was added in
  `docs/notes/`.
- The wire format between client and host (spec file, hello/welcome lines,
  `_`-prefixed extension methods, journal lines) is versioned by
  `Protocol`. A change that breaks an older client or host bumps it.
- Linux only for now (unix sockets, flock, process groups). `CGO_ENABLED=0`
  must build.
- Design notes go in a new file `docs/notes/<issue>-<slug>.md` (short,
  dated). Never append numbered sections to a shared doc.
- Releases are git tags `vX.Y.Z`. Breaking API changes before v1 bump the
  minor version.
- Plain names, short comments explaining why, no marketing language.
