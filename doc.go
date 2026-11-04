// Package acphost runs an ACP agent in a detached host process that owns
// the agent, proxies ACP over a unix socket, journals every frame and
// outlives its client.
package acphost
