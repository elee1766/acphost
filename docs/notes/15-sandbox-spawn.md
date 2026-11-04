# #15 Running the agent through bwrap (2026-11-04)

When `Spec.Sandbox` is set, the host runs `SandboxArgs(...)` instead of the
agent command. The host stays outside the sandbox.

- Process: bwrap is started with the same Setpgid and Pdeathsig as a bare
  agent. Stop signals bwrap's process group. bwrap forwards nothing, but
  `--die-with-parent` plus `--unshare-pid` means that when bwrap dies its
  pid 1 (the agent) dies and the kernel kills the rest of the namespace.
- Exit code: bwrap exits with the agent's code (checked in
  `TestSandboxExitCode`) and reports a signal death as 128+n. The host maps
  128+n back to `Signal`, ExitCode -1, so exit.json reads the same as for an
  unsandboxed agent. `--json-status-fd` was not needed.
- Env: bwrap clears the env. `Spec.Env` entries are added with `--setenv`
  (before `ExtraArgs`) because the caller asked the agent to have them.
- Dir: bwrap does the chdir. `Dir` is a path inside the sandbox, so the host
  does not set `cmd.Dir` and `CheckSpawn` does not stat it on the host;
  `SandboxArgs` checks it is under an RW mount.
- `CheckSpawn` validates via `SandboxArgs` (bwrap lookup, mounts, Dir) and
  returns `*PermanentError`. The agent command is resolved inside the
  sandbox, so it is not looked up on the host.
- Tests skip unless bwrap works. CI needs `sudo apt-get install -y
  bubblewrap` in .github/workflows/ci.yml, which a human has to push.
