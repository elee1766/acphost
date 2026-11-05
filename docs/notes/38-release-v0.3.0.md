# #38 Release v0.3.0

2026-11-05

The README now starts with a quick start, followed by one section each for
Sandbox, pasta network, User namespace and Cgroup, each with its one-time
root setup. The v0.3.0 release notes are in the README. Protocol stays 1:
the new spec fields (`Sandbox.Network` pasta, `Spec.User`, `Spec.Cgroup`)
are optional. No breaking API changes.

Install: `go get github.com/elee1766/acphost@v0.3.0`
