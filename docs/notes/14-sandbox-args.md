# #14 SandboxArgs (2026-11-04)

`Spec.Sandbox` (nil = no sandbox) describes a bubblewrap sandbox.
`SandboxArgs` turns it into a bwrap argv. Nothing spawns it yet (#13).

- Env: always `--clearenv`, then `--setenv` for each `EnvKeep` name that is
  set on the host. An agent should not inherit host secrets by accident,
  so the allowlist is explicit.
- Order: binds and tmpfs mounts are stably sorted by destination depth.
  bwrap applies mounts in argv order, so parents must come first, and at
  the same path the later entry wins (RW listed after RO stays writable).
  Hide tmpfs mounts come after all binds so they cover them.
- A missing RO source fails unless `skip_missing` is set (for optional
  paths like a per-user config). A missing RW source always fails.
- `dir` must be under an RW destination, so the agent can write where it
  starts.
- Mounts unmarshal from a plain path string or `{"src","dst"}`.
- Protocol stays 1: the field is optional and older hosts ignore it.
