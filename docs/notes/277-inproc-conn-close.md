# 277: close client connections when Main returns

2026-11-09

Problem: a real host process closes every client connection by exiting.
An in-process host (acphosttest) kept them open after Main returned, because
serveConn only closed its conn when its own read loop ended. A client blocked
in a read never saw the exit. jobshop worked around it with
`control.HostExits`.

Confirmed on master first: the new test `TestAgentExitClosesClients` timed
out reading from a connected client.

Fix: the accept loop records each conn in `Host.conns` (own mutex, since
serveConn holds `Host.mu` while writing). `run` closes them all after the
listener closes, which also ends the handler goroutines. A conn accepted
after that point is closed at once (`connsClosed`), so none slip through.
The same applies to a connection that has not sent its hello yet.

No wire change, `Protocol` stays 1.
