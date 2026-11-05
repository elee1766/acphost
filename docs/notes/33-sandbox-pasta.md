# #33 Sandbox network "pasta" with deny CIDRs (2026-11-05)

`Sandbox.Network = "pasta"` gives the agent its own network namespace with
user-mode networking from pasta (passt). `Sandbox.Deny` blocks CIDRs inside
it.

## Attach: pasta is bwrap's parent

The argv is `pasta --config-net -f -q -t none -u none -T none -U none --
[sh -c <load rules> ...] bwrap ... -- agent`. pasta creates a user and
network namespace, brings up and configures the interface, and only then
starts its command. So the agent's first connect always sees a configured
interface and loaded rules. There is no race to close.

Attaching pasta to the agent's namespace by pid (`bwrap --unshare-net`,
then `pasta <pid>`) needs a sync step: bwrap would have to block (for
example `--block-fd`) until pasta is up and the rules are in, and the host
would have to start, watch and stop a second process. Running bwrap under
pasta needs none of that:

- Lifetime: pasta runs in the foreground and exits when its child exits,
  passing the exit code through. Stop signals the host's process group
  (pasta is the group leader), and Pdeathsig still kills pasta if the host
  dies. bwrap keeps `--die-with-parent`.
- bwrap does not get `--unshare-net` for pasta: the namespace it inherits
  is already private.
- Exit codes: bwrap reports a signal death as 128+n, and pasta passes that
  through, so exit.json reads the same. A failure to load rules exits 125.
- No inbound port forwards (`-t none` and the rest): the agent only dials
  out.

## Deny: nftables, not pasta options

pasta has no option to refuse destinations. It can bind outbound to an
interface or address, which does not filter. So the host runs `nft` inside
the namespace before exec'ing bwrap: one `inet acphost` table whose output
chain rejects the denied v4 and v6 prefixes. Rejecting, not dropping,
makes a denied connect fail at once instead of timing out.

nft runs as root in pasta's user namespace, so no host privilege is needed.
bwrap adds `--cap-drop ALL` for pasta networking. Without it the agent
keeps CAP_NET_ADMIN in that user namespace and could flush the rules
(checked by hand: with the drop, `nft flush ruleset` gets EPERM).

`nft` is needed only when Deny is set. CIDRs are parsed with
`netip.ParsePrefix` and masked, so `10.1.2.3/8` becomes `10.0.0.0/8`.
`Deny` with any network but pasta is a validation error.

## Checks

`CheckSpawn` reports a missing pasta (or nft, with Deny) as a
`PermanentError`. `TestSandboxPastaDeny` dials a listener on host loopback
through the default gateway, which pasta maps to the host. It skips
without bwrap, pasta, nft or user namespaces.
