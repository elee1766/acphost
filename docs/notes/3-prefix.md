# #3 Caller-settable extension method prefix and GC root link name

2026-11-04

- `Spec.MethodPrefix` (default `DefaultMethodPrefix`, `_jobshop/`) sets
  the prefix of the `stop`, `turn_end` and `export_stats` extension
  methods. `MethodName(prefix, name)` builds a name. The `Method*`
  constants keep the default names.
- `Spec.GCRootPrefix` (default `DefaultGCRootPrefix`, `jobshop-host-`)
  sets the GC root link name, `<prefix><session>`.
- The host reports its prefix in `Welcome.method_prefix`.
  `Connect(ctx, dir, WithMethodPrefix(p))` fails with a clear error if it
  differs. An old host sends no field, which reads as the default, so a
  new controller with default settings still talks to it. An old
  controller ignores the field.
- Both spec fields are omitempty, so default specs are byte-identical to
  before. Protocol stays 1: nothing breaks an older client or host.
