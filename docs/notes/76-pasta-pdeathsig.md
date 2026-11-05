# 76: agent outlived a killed host under pasta

2026-11-05

On ubuntu-26.04 (kernel 7.0, passt 2026-01-20, bwrap 0.11.1)
TestMatrixPdeathsig failed for the pasta flavours: after SIGKILL of the
host, pasta was reparented to init and kept bwrap and the agent alive. A
process tree dump from the runner showed pasta itself surviving, not a
stray match on the agent's command line.

The host starts the agent with Pdeathsig SIGKILL, but the kernel clears
the parent-death signal on a credential change, and pasta makes and
enters its own user namespace before running its command. The likely
cause is that newer pasta (or kernel 7.0) does this in the process the
host started, so the signal is gone. The fix does not depend on which.
bwrap's --die-with-parent only follows pasta, so nothing reached the
agent. On 24.04 the older pasta kept the signal and the test passed.

Fix: with network pasta the host now re-execs itself as a small helper
(`_ACPHOST_PDEATH_CHILD`, like the userns helper) that sets its own
parent-death signal, starts pasta, and on host death SIGKILLs its process
group (pasta and the outer bwrap; bwrap's --die-with-parent is lost the
same way, as the first CI run of the fix showed, and the outer bwrap is
pid 1 of the sandbox pid namespace, so the agent dies with it). It
forwards SIGTERM/SIGINT/SIGHUP and exits with pasta's status. isolation.json
still records the bwrap/pasta argv without the helper. CI runs on
ubuntu-26.04 again, and the test logs the surviving process tree if it
fails.
