# 163: sandbox overlay mounts

2026-11-06

`Sandbox.Overlay []Overlay` merges host dirs onto one destination with
bwrap's unprivileged overlays (bwrap 0.10+): `--overlay-src` per layer,
lowest first, then `--ro-overlay` (mode `ro`, default), `--tmp-overlay`
(`tmp`, writes lost) or `--overlay UPPER WORK DST` (`persist`).

- Overlays sort with the binds by destination depth, so parents mount
  first; at equal depth input order holds (RO, RW, Tmpfs, then Overlay).
- Checked at argv build time: Dst and layers absolute, layers exist and
  are dirs (no SkipMissing), 1 to 500 layers, mode known. For `persist`,
  Upper and Work absolute, existing, same filesystem (st_dev), not nested
  in each other or in a layer, Work empty. Upper/Work outside `persist`
  is an error.
- bwrap reads layers from the host, so an overlay cannot use another
  overlay as a layer. Callers pass one flat layer list per destination.
- Nothing new in isolation.json: the flags are in `Bwrap`.
- Works with a mapped `User`: bwrap mounts before the agent runs as the
  inside uid (TestUsernsOverlay).
