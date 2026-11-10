# 285: optional TCP listener, mutual TLS or none

2026-11-10.

`Spec.Listen` (`host:port`) makes `Main` listen on TCP next to `acp.sock`.
`Spec.TLS` turns on mutual TLS. Code: `pkg/acphost/listen.go` (spec,
reloading, dead peers), `listen_client.go` (`ConnectTCP` options), the
accept loop and `serveConn` in `host.go`.

## Decisions

- **Same accept loop.** The TCP listener is served by `acceptLoop` and
  `serveConn`. TLS is `tls.Server` around the accepted conn, handshaken
  first thing in `serveConn`, so a refused client is logged with its peer
  and reason and never reaches the controller swap. Controller
  replacement, `closeConns` (#277), replay and the lease need no new code.
- **Order in `Main`.** TCP opens first, then `acp.sock`, then the agent.
  A client that sees the socket accept can read `listen.json`. Both
  listeners are up before the agent starts.
- **Rotation by `GetConfigForClient`.** Each new connection reads
  `cert`, `key` and `client_ca` again and builds its own `tls.Config`.
  A read or parse error keeps the last good set and logs a warning on
  each such handshake. No watcher, no timer. A half-replaced pair
  (new cert, old key) fails the parse and so keeps the last set.
  `Main` also loads the files once at start, so a bad set fails the
  start; `CheckSpawn` does the same as a `PermanentError`.
- **Allow list.** A `VerifyPeerCertificate` on the per-connection config,
  which runs after the chain is verified. DNS SANs match
  case-insensitively, URI SANs exactly. A certificate with neither is
  refused when `Allow` is set.
- **Plaintext mode** is a warning at start and nothing else. No token.
- **No Protocol bump.** Hello and welcome are the same bytes inside TLS
  or not. An older client does not know the TCP listener, it just never
  uses it. `listen` and `tls` are `omitempty` spec keys an older host
  ignores (it would then not listen, which a caller sees from the missing
  `listen.json`).
- **Files stay with the host.** They are read by the host process only;
  nothing adds them to the sandbox or the agent's env (test:
  `TestTLSPathsNotInAgentEnv`).
- **Logger.** `WithLogger` is new, so tests can wait on a log line (the
  refusal and the warnings) instead of sleeping.

## Dead peers

A peer that vanishes (power loss, a cut route) sends no FIN. Accepted
conns get:

- keepalive: 30s idle, then 3 probes 10s apart, so an idle dead peer is
  found 60s after the idle time ends: at most 90s after the last
  traffic;
- `TCP_USER_TIMEOUT` 60s: data written and not acknowledged for 60s
  closes the connection. This is the case that matters while the host
  has something to send (a prompt answer, `session/update`).

Either way the read in `serveConn` returns an error, `dropConn` sets
`h.conn = nil` and stamps `disconnected`, and the lease runs from there.
Bound: a lost peer is dropped at most ~90s after it was last heard from
(idle) or 60s after the first unacknowledged write. A black-holed peer
before that bound still holds the controller slot, but a new controller
replaces it at once, as with the socket. If `TCP_USER_TIMEOUT` cannot be
set the host logs a warning and keepalive alone applies.

## Tests

Real certificates from `crypto/x509` (`listen_helpers_test.go`), a real
host on `127.0.0.1:0`, no sleeps: waits are on log lines, the journal and
`Done`. `TestListenTCPDeadPeerOptions` reads the socket options back. The
timeout itself is not exercised end to end, it needs a black-holed route.
