package proc

import (
	"fmt"
	"slices"
	"syscall"

	"github.com/elee1766/acphost/pkg/trace"
	"github.com/elee1766/acphost/pkg/xerr"
)

// JoinTypes lists the namespace types Spec.Join accepts, in the order
// they are joined: user first (it grants the caps for the rest), mnt
// last (it changes how later paths resolve).
var JoinTypes = []string{"user", "cgroup", "ipc", "uts", "net", "pid", "mnt"}

// nsenter long flag per type.
var nsenterFlags = map[string]string{
	"user": "--user", "cgroup": "--cgroup", "ipc": "--ipc", "uts": "--uts",
	"net": "--net", "pid": "--pid", "mnt": "--mount",
}

// JoinApplied is one joined namespace in isolation.json: the path from
// Spec.Join and the namespace it resolved to, as "net:[4026531840]".
type JoinApplied struct {
	Path string `json:"path"`
	NS   string `json:"ns"`
}

// joinPrefix returns the nsenter argv that joins j and then runs what
// follows it, and what each path resolved to. wdns, if set, is the
// working directory inside the joined mount namespace. Credentials are
// kept: in a joined user namespace the command has whatever ids its
// mapping gives the caller. With pid, nsenter forks and the command is
// its child, since setns(pid) only affects children.
func joinPrefix(j map[string]string, wdns string) ([]string, map[string]JoinApplied, error) {
	nsenter, err := trace.LookPath("nsenter")
	if err != nil {
		return nil, nil, xerr.Permanentf("join: %w", err)
	}
	argv := []string{nsenter}
	applied := map[string]JoinApplied{}
	for _, t := range JoinTypes {
		p, ok := j[t]
		if !ok {
			continue
		}
		var st syscall.Stat_t
		if err := syscall.Stat(p, &st); err != nil {
			return nil, nil, xerr.Permanentf("join.%s: %w", t, err)
		}
		applied[t] = JoinApplied{Path: p, NS: fmt.Sprintf("%s:[%d]", t, st.Ino)}
		argv = append(argv, nsenterFlags[t]+"="+p)
	}
	if _, ok := j["user"]; ok {
		argv = append(argv, "--preserve-credentials")
	}
	if wdns != "" {
		argv = append(argv, "--wdns="+wdns)
	}
	return append(argv, "--"), applied, nil
}

// dropUnsharePid removes bwrap's --unshare-pid when the command joins a
// pid namespace instead.
func dropUnsharePid(argv []string) []string {
	return slices.DeleteFunc(argv, func(a string) bool { return a == "--unshare-pid" })
}
