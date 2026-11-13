# Session commands move to acpc

2026-11-13

The CLI's named agent sessions (`new`, `prompt`, `exec`, `attach`,
`cancel`, `ls`, `rm`, and `-s NAME` on `status`, `stop` and `wait`),
added in `290-cli-sessions.md`, moved to
[gfx-labs/acpc](https://github.com/gfx-labs/acpc) with their history.

Why: acphost is a library for running an agent in a detached, isolated
host. An ACP client is a separate program with its own dependencies (it
now speaks ACP through `github.com/gfx-labs/go-acp`) and release cycle.
acpc uses only acphost's public API.

What stays: the library, and `run`, `host`, `start`, `status`, `stop`
and `wait` on `--dir`, for debugging isolation without a controller.
urfave/cli stays for those commands.

Breaking for CLI users: the session commands and `-s` are gone from
`acphost`. Use `acpc`, whose sessions are under
`$XDG_STATE_HOME/acpc/sessions`. No library API changed and `Protocol`
stays 1.
