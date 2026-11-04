# #9 Release v0.1.0

2026-11-04

First tagged release of acphost. Contents:

- Port of jobshop `pkg/host`: spawn, socket proxy, journal, replay,
  lease (#2).
- `Exporter` hook for conversation export, so the library has no
  telemetry or jobshop dependency (#2).
- Caller-set `Spec.MethodPrefix` and `Spec.GCRootPrefix` (#3).
- Doc comments on all exports and a README usage example (#4).
- Stop reason recorded in `exit.json` for controller stop and lease
  expiry (#8).

`Protocol` stays 1. Wire names (spec file, hello/welcome lines,
`_jobshop/` extension methods, journal lines) are unchanged from
jobshop `pkg/host`, so existing jobshop hosts and controllers
interoperate.

Install: `go get github.com/elee1766/acphost@v0.1.0`
