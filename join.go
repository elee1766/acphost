package acphost

import (
	"fmt"
	"os"
	"slices"
	"syscall"

	"golang.org/x/sys/unix"
)

// JoinTypes lists the namespace types Spec.Join accepts, in the order
// they are joined: user first (it grants the caps for the rest), mnt
// last (it changes how later paths resolve).
var JoinTypes = []string{"user", "cgroup", "ipc", "uts", "net", "pid", "mnt"}

var nsFlags = map[string]int{
	"user":   unix.CLONE_NEWUSER,
	"cgroup": unix.CLONE_NEWCGROUP,
	"ipc":    unix.CLONE_NEWIPC,
	"uts":    unix.CLONE_NEWUTS,
	"net":    unix.CLONE_NEWNET,
	"pid":    unix.CLONE_NEWPID,
	"mnt":    unix.CLONE_NEWNS,
}

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

// checkJoin checks Spec.Join: known types, no type both joined and
// created, each path a namespace of its type, and CAP_SYS_ADMIN over
// each (owner uid is ours and it is not our own user namespace), so a
// bad join fails here instead of as a bare EPERM from the spawn.
func checkJoin(s Spec) error {
	if len(s.Join) == 0 {
		return nil
	}
	for t := range s.Join {
		if _, ok := nsFlags[t]; !ok {
			return Permanentf("join: unknown namespace type %q (want one of %v)", t, JoinTypes)
		}
	}
	// The userns helper creates the user namespace at clone, before
	// nsenter runs inside it, and that new namespace has no caps over
	// the ones being joined. Join a user namespace instead.
	if s.User != nil {
		return Permanentf("join: cannot combine join with user; put the ids in a user namespace and use join.user")
	}
	if _, ok := s.Join["net"]; ok && s.Sandbox != nil && (s.Sandbox.Network == "none" || s.Sandbox.Network == "pasta") {
		return Permanentf("join: join.net conflicts with sandbox network %q, which makes its own network namespace", s.Sandbox.Network)
	}
	if _, err := lookPath("nsenter"); err != nil {
		return Permanentf("join: %w", err)
	}
	self, err := nsIno("/proc/self/ns/user")
	if err != nil {
		return Permanentf("join: %w", err)
	}
	euid := os.Geteuid()
	_, joinUser := s.Join["user"]
	for _, t := range JoinTypes {
		p, ok := s.Join[t]
		if !ok {
			continue
		}
		if err := checkNS(t, p, joinUser, self, euid); err != nil {
			return err
		}
	}
	return nil
}

func checkNS(t, p string, joinUser bool, self uint64, euid int) error {
	fd, err := unix.Open(p, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return Permanentf("join.%s %s: %w", t, p, err)
	}
	defer func() { _ = unix.Close(fd) }()
	typ, err := unix.IoctlRetInt(fd, unix.NS_GET_NSTYPE)
	if err != nil {
		return Permanentf("join.%s %s: not a namespace: %w", t, p, err)
	}
	if typ != nsFlags[t] {
		return Permanentf("join.%s %s: is a %s namespace", t, p, nsName(typ))
	}
	if euid == 0 || (t != "user" && joinUser) {
		// root has the caps; the rest are checked against join.user.
		return nil
	}
	ufd := fd
	if t != "user" {
		// setns of any other type also needs CAP_SYS_ADMIN in our own
		// user namespace, which a non-root caller only gets by joining
		// (or being in) a user namespace it owns.
		if ufd, err = unix.IoctlRetInt(fd, unix.NS_GET_USERNS); err != nil {
			return Permanentf("join.%s %s: owning user namespace: %w", t, p, err)
		}
		defer func() { _ = unix.Close(ufd) }()
		owner, _ := unix.IoctlGetUint32(ufd, unix.NS_GET_OWNER_UID)
		return Permanentf("join.%s %s: needs CAP_SYS_ADMIN, which uid %d does not have here; also set join.user to the namespace's owning user namespace (owner uid %d)", t, p, euid, owner)
	}
	owner, err := unix.IoctlGetUint32(ufd, unix.NS_GET_OWNER_UID)
	if err != nil {
		return Permanentf("join.%s %s: owner uid: %w", t, p, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(ufd, &st); err != nil {
		return Permanentf("join.%s %s: %w", t, p, err)
	}
	if int(owner) != euid || st.Ino == self {
		return Permanentf("join.%s %s: needs CAP_SYS_ADMIN in its user namespace (owner uid %d, our uid %d); join a user namespace you own first", t, p, owner, euid)
	}
	return nil
}

func nsName(flag int) string {
	for t, f := range nsFlags {
		if f == flag {
			return t
		}
	}
	return fmt.Sprintf("unknown (%#x)", flag)
}

func nsIno(p string) (uint64, error) {
	var st unix.Stat_t
	if err := unix.Stat(p, &st); err != nil {
		return 0, err
	}
	return st.Ino, nil
}

// joinPrefix returns the nsenter argv that joins j and then runs what
// follows it, and what each path resolved to. wdns, if set, is the
// working directory inside the joined mount namespace. Credentials are
// kept: in a joined user namespace the agent has whatever ids its
// mapping gives the host user. With pid, nsenter forks and the agent is
// its child, since setns(pid) only affects children.
func joinPrefix(j map[string]string, wdns string) ([]string, map[string]JoinApplied, error) {
	nsenter, err := lookPath("nsenter")
	if err != nil {
		return nil, nil, Permanentf("join: %w", err)
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
			return nil, nil, Permanentf("join.%s: %w", t, err)
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

// dropUnsharePid removes bwrap's --unshare-pid when the agent joins a pid
// namespace instead.
func dropUnsharePid(argv []string) []string {
	return slices.DeleteFunc(argv, func(a string) bool { return a == "--unshare-pid" })
}

// JoinPlan describes the namespaces s joins in join order, one
// "type=path" per entry, for a dry-run print.
func JoinPlan(s Spec) []string {
	var out []string
	for _, t := range JoinTypes {
		if p, ok := s.Join[t]; ok {
			out = append(out, t+"="+p)
		}
	}
	return out
}
