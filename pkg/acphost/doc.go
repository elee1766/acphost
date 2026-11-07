// Package acphost is a Go library for the Agent Client Protocol (ACP).
// It runs a coding agent such as jcode (jcode acp), Claude Code (via
// claude-code-acp) or Gemini CLI (gemini --experimental-acp) in a
// detached host process, so the agent session survives client restarts.
//
// One host process per session spawns the agent and owns its process
// group, proxies ACP between the agent's stdio and a unix socket
// (acp.sock), journals every frame so a reconnecting client can replay
// what it missed, and exits when no client has connected for a lease
// period.
//
// A client writes a [Spec] with [WriteSpec], starts the host with
// [StartDetached] (which runs [Main] in a detached re-exec of the
// program; call [MaybeChild] first in main), and talks ACP over the
// socket returned by [Connect].
// After a restart it reconnects with [Hello].Since set to the last seq it
// saw. See the package example.
//
// The isolation a [Spec] can ask for is described by the sandbox, cgroup
// and userns packages, and pkg/proc runs the same isolation for any
// command without ACP. [Preflight] checks the host for it before a spawn.
package acphost
