# Root isolation aliases removed (#160)

2026-11-06

Step 5 of #155. The root package `pkg/acphost` no longer has the aliases
added in #156 for the moved isolation API:

| removed | use |
|---|---|
| `Sandbox`, `DNS`, `Mount` | `sandbox.Sandbox`, `sandbox.DNS`, `sandbox.Mount` |
| `SandboxArgs`, `CheckSandboxDNS` | `sandbox.Args`, `sandbox.CheckDNS` |
| `Cgroup`, `CgroupStats`, `DelegatedInfo`, `ReasonOOM` | `cgroup.Cgroup`, `cgroup.Stats`, `cgroup.DelegatedInfo`, `cgroup.ReasonOOM` |
| `DelegatedCgroup`, `SetupDelegation` | `cgroup.Delegated`, `cgroup.SetupDelegation` |
| `User`, `Range`, `Alloc`, `ParseSubIDs`, `AllocSubIDs` | the same names in `userns` |

`PermanentError` and `Permanentf` stay: they are acphost's error type,
not an alias for a moved feature.

The aliases were the same types, so the JSON of `Spec` does not change
and no `Protocol` bump is needed. Breaking for Go callers: minor bump.

The issue asked to wait one release after jobshop moved. jobshop's only
uses were in pkg/rpc/rpc.go, switched by elee1766/jobshop#793 (closed
2026-11-06). jobshop is on v0.12.0, which still has the aliases, so
nothing breaks until it bumps past this release, and its code no longer
needs them when it does.
