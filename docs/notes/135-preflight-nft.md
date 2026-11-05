# Preflight checks nft for sandbox Deny (#135)

2026-11-05

Spawn with `sandbox.deny` runs `nft` (looked up on PATH) inside the pasta
namespace to load the reject rules. Preflight did not check for it, so a
host without nftables passed preflight and then failed every spawn with
`sandbox: deny: exec: "nft": executable file not found in $PATH`.

`Preflight` now adds a `network.nft` check when Deny is non-empty, with an
install hint (apt/dnf install nftables). Both sides use the `nftBinary`
constant, resolved on PATH, so they cannot drift. There is no
`sandbox.nft` override: spawn has none either. No Deny, no check.
