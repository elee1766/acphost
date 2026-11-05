# Preflight sandbox.dns check (#136)

2026-11-05

`CheckSandboxDNS` (#124) spawns pasta, so `Preflight` does not run it by
default. `Preflight(spec, acphost.WithDNSCheck("example.com"))` adds a
`sandbox.dns` check (feature `sandbox`) that resolves the name the way an
agent would, inside a pasta namespace with the spec's DNS forward and
deny rules.

- Only when `sandbox.network` is `pasta`.
- Skipped when `network.pasta` failed: pasta is missing and that check
  already says so.
- A failure's hint names `sandbox.dns.forward` and `sandbox.resolv_conf`
  (a loopback stub resolver is not reachable from the namespace).

Why: two sessions on 2026-11-05 died on "Temporary failure in name
resolution" that this check catches at load time. The existing
`Preflight(spec)` call is unchanged (variadic options).
