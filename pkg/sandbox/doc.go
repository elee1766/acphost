// Package sandbox describes a bubblewrap sandbox (mounts, overlays, env,
// network with pasta and deny rules, DNS) and builds the bwrap argv for
// it. [Sandbox] is the description and [Args] (or [BuildArgs], which also
// reports the network setup) builds the command line. [CheckDNS] checks
// that DNS works inside a pasta namespace.
//
// Most callers set [Sandbox] on a spec and let pkg/acphost or pkg/proc
// (Start) run it. Pasta networking and the DNS check re-exec the calling
// binary, so the program must call proc.MaybeChild first in main.
package sandbox
