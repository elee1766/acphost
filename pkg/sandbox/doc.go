// Package sandbox describes a bubblewrap sandbox (mounts, env, network
// with pasta and deny rules, DNS) and builds the bwrap argv for it. It
// also checks that DNS works inside a pasta namespace.
package sandbox
