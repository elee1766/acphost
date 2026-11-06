# 164: pkg/ layout

2026-11-06

All Go packages moved under `pkg/` and `internal/` is gone, so the repo
has one layout: libraries in `pkg/`, binaries in `cmd/` (the CLI, #165).

The root package went to `pkg/acphost`, not `pkg/host`: the package name
stays `acphost`, so callers change only the import path, not every
`acphost.X` reference, and the name matches the module. `pkg/host` would
also clash with jobshop's own `pkg/host`, the package that imports it.

Old to new import paths (pre-v1, so a minor version bump):

| old | new |
|---|---|
| `github.com/elee1766/acphost` | `github.com/elee1766/acphost/pkg/acphost` |
| `github.com/elee1766/acphost/acphosttest` | `github.com/elee1766/acphost/pkg/acphosttest` |
| `github.com/elee1766/acphost/cgroup` | `github.com/elee1766/acphost/pkg/cgroup` |
| `github.com/elee1766/acphost/proc` | `github.com/elee1766/acphost/pkg/proc` |
| `github.com/elee1766/acphost/sandbox` | `github.com/elee1766/acphost/pkg/sandbox` |
| `github.com/elee1766/acphost/userns` | `github.com/elee1766/acphost/pkg/userns` |
| `internal/hostenv`, `internal/trace`, `internal/xerr`, `internal/reexec` | `pkg/hostenv`, `pkg/trace`, `pkg/xerr`, `pkg/reexec` |

The former internal packages are importable now. Their doc comments say
they serve acphost itself and are not a stable API; their surface is
unchanged (no new exports). No new aliases were added. No wire format
change: `Protocol` is unchanged.

Callers: `sed` the import paths above. jobshop updates its imports and
bumps acphost in one PR.
