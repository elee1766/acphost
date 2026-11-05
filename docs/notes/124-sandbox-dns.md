# Sandbox DNS for pasta (#124)

2026-11-05

With `network: pasta` the agent has its own network namespace. On a host
whose /etc/resolv.conf names a loopback stub (systemd-resolved's
127.0.0.53), the stub is the namespace's own loopback, so every lookup
fails. The upstream file can list no nameservers at all (Tailscale
MagicDNS sits behind the stub), so copying it in does not help.

acphost adds two generic settings and no resolver logic: no detection of
systemd-resolved, no default resolver, no public fallback (operator
decision). Unset keeps the old behavior.

- `Sandbox.DNS` (`dns: {forward, host}`): passed to pasta as
  `--dns-forward <forward>` and, when set, `--dns-host <host>`. pasta
  answers queries to `forward` inside the namespace by relaying them to
  `host` on the host side, where a loopback stub is reachable. Both must
  be IP addresses. Only valid with pasta.
- `Sandbox.ResolvConf` (`resolv_conf: <path>`): a host file bound
  read-only over /etc/resolv.conf, after all other binds so an /etc bind
  does not cover it. The caller writes it (usually `nameserver <forward>`
  plus its own `search`/`options` lines).
- The forward address must not fall inside a `Deny` CIDR: the nft rules
  apply to it like any destination, so `SandboxArgs` rejects that spec.
  169.254.1.1 is outside common deny lists (10/8, 100.64/10, 192.168/16);
  the host side (100.100.100.100 behind the stub, for example) is not
  filtered since pasta makes that connection outside the namespace.
- `isolation.json` network gains `dns_forward`, `dns_host` and
  `resolv_conf`.
- `CheckSandboxDNS(sandbox, name)`: resolves `name` inside a pasta
  namespace set up like the agent's, using the first nameserver of
  `ResolvConf` (or the host's /etc/resolv.conf). It re-execs the host
  binary as the resolver, so it needs nothing inside a sandbox. A
  failure is a PermanentError naming `sandbox.dns` and
  `sandbox.resolv_conf`. It spawns pasta, so callers run it once at load
  time; it is not part of `CheckSpawn`.

New optional JSON fields in the spec: an older host ignores them (and
then has the old DNS behavior), so no Protocol bump.

## Example: a systemd-resolved host

Consumer config, not a default:

```
# /etc/acphost/resolv.conf
nameserver 169.254.1.1
search tailnet.example
```

```json
"sandbox": {
  "network": "pasta",
  "dns": {"forward": "169.254.1.1", "host": "127.0.0.53"},
  "resolv_conf": "/etc/acphost/resolv.conf"
}
```

## Test

`TestSandboxPastaDNS` runs a fake loopback resolver on 127.0.0.77:53
(CI lowers `net.ipv4.ip_unprivileged_port_start` so it can bind; it skips
elsewhere). Using the runner's real 127.0.0.53 stub instead was flaky,
since it answers from the network (#131). The test shows `CheckSandboxDNS` fails
without `dns`, then resolves with it, and runs a real agent whose
`getent hosts` resolves through the bound resolv.conf with deny rules for
10/8, 100.64/10 and 192.168/16 loaded.
