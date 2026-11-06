# #151 Release v0.10.0

2026-11-06

v0.10.0 marks the first release jobshop runs on with the Preflight host
checks adopted. Changes over v0.8.x, all shipped in v0.9.0 and taken up by
jobshop:

- `Preflight` checks that `nft` is present when `sandbox.deny` is set
  (#135, #138).
- `Preflight(spec, WithDNSCheck(host))` resolves a name inside the
  sandbox network to check `sandbox.dns` (#136, #139).
- Journal follow uses inotify instead of 200ms polling (#137, #141).
- jobshop is on acphost v0.9.0 and runs Preflight with the nft and DNS
  checks at load (elee1766/jobshop#748, elee1766/jobshop#752).

No API or wire change since v0.9.0. Protocol stays 1.

Release: the PR that adds this note is a `fix` commit with a
`Release-As: 0.10.0` footer, since a chore commit alone does not trigger
release-please (see #145). release-please-config.json is not changed, so
there is no release-as to drop afterwards.

Install: `go get github.com/elee1766/acphost@v0.10.0`
