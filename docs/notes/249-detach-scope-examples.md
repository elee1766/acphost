# 249: examples for InScope and InCgroup

2026-11-07. Part of target #246.

`example_detach_test.go` showed `StartDetached` without options. It now
has `ExampleInScope` and `ExampleInCgroup`, each saying when to choose it:

- `InScope(slice)`: the caller runs as a systemd unit that restarts, or
  anything else with a systemd manager to ask. The host sits in a
  transient scope outside the unit, so the unit empties on stop (note 240).
- `InCgroup(dir)`: delegated cgroupfs with no manager to ask (a unit with
  `Delegate=yes`, a container, root). The host counts against that
  directory (note 223).

Neither has an `// Output:` line, so `go test` compiles them and does not
run them: they need a systemd manager or a delegated cgroup, and an agent.
They are attached to `InScope` and `InCgroup` by name, so `pkg.go.dev`
shows them under those functions. `go doc` does not print examples (it
skips `_test.go` files), so they do not show in `go doc`.

README: the "Detached host" paragraphs for each option and the driver
list under "Choosing a driver" link the examples.

Docs only. No API or `Protocol` change.
