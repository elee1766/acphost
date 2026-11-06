package userns

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"slices"
	"strconv"
	"strings"

	"github.com/elee1766/acphost/pkg/hostenv"
	"github.com/elee1766/acphost/pkg/trace"
	"github.com/elee1766/acphost/pkg/xerr"
)

// User on a Spec runs the agent as UID:GID inside a user
// namespace. The inside ids map to MapUID and MapGID outside, which the
// caller must own: its own id, a gid it holds, or an id in its
// /etc/subuid or /etc/subgid range. Groups are supplementary gids, each
// mapped to the same gid outside.
//
// MapCount, when above 1, reserves the outside ids
// [MapUID, MapUID+MapCount) and [MapGID, MapGID+MapCount) for the
// session (as AllocSubIDs does). Each must lie inside one subuid or
// subgid entry; only the first id is mapped.
type User struct {
	UID      int   `json:"uid"`
	GID      int   `json:"gid"`
	MapUID   int   `json:"map_uid"`
	MapGID   int   `json:"map_gid"`
	MapCount int   `json:"map_count,omitempty"`
	Groups   []int `json:"groups,omitempty"`
}

// Range is one subordinate id range from /etc/subuid or /etc/subgid.
type Range struct {
	Start, Count int
}

func (r Range) contains(id int) bool { return id >= r.Start && id-r.Start < r.Count }

// ParseSubIDs reads subuid/subgid lines ("name:start:count") and returns
// the ranges whose owner is name or the numeric uid. Comments and blank
// lines are skipped; malformed lines are an error.
func ParseSubIDs(r io.Reader, name string, uid int) ([]Range, error) {
	var out []Range
	uidStr := strconv.Itoa(uid)
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, ":")
		if len(f) != 3 {
			return nil, fmt.Errorf("line %d: want owner:start:count", n)
		}
		start, err1 := strconv.Atoi(f[1])
		count, err2 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil || start < 0 || count < 0 {
			return nil, fmt.Errorf("line %d: bad start or count", n)
		}
		if f[0] == uidStr || (name != "" && f[0] == name) {
			out = append(out, Range{Start: start, Count: count})
		}
	}
	return out, sc.Err()
}

// ReadSubIDs parses path for uid. A missing file means no ranges.
func ReadSubIDs(path string, uid int) ([]Range, error) {
	trace.Event("subid:" + path)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	name := ""
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		name = u.Username
	}
	return ParseSubIDs(f, name, uid)
}

// InRanges reports whether id lies in one of rs.
func InRanges(id int, rs []Range) bool {
	return slices.ContainsFunc(rs, func(r Range) bool { return r.contains(id) })
}

// CheckSpan returns a PermanentError unless [start, start+n) lies inside
// a single entry of rs. Adjacent entries do not join: newuidmap checks
// each map line against one entry.
func CheckSpan(kind, file string, start, n int, rs []Range) error {
	if slices.ContainsFunc(rs, func(r Range) bool {
		return start >= r.Start && start+n <= r.Start+r.Count
	}) {
		return nil
	}
	ents := make([]string, len(rs))
	for i, r := range rs {
		ents[i] = fmt.Sprintf("%d..%d", r.Start, r.Start+r.Count-1)
	}
	if len(ents) == 0 {
		ents = []string{"none"}
	}
	return xerr.Permanentf("user: %ss %d..%d are not inside one %s entry (entries: %s)", kind, start, start+n-1, file, strings.Join(ents, ", "))
}

// Maps builds the id map arguments for newuidmap and newgidmap (the
// part after the pid: inside outside count, repeated). self is the
// caller's uid. Ids the caller cannot map are a PermanentError. Output is
// sorted so the same User always gives the same arguments.
func Maps(u User, self int, subuidFile, subgidFile string, subuid, subgid []Range) (uidArgs, gidArgs []string, err error) {
	if u.UID < 0 || u.GID < 0 || u.MapUID < 0 || u.MapGID < 0 || u.MapCount < 0 {
		return nil, nil, xerr.Permanentf("user: negative id")
	}
	if u.MapCount > 1 {
		if err := CheckSpan("uid", subuidFile, u.MapUID, u.MapCount, subuid); err != nil {
			return nil, nil, err
		}
		if err := CheckSpan("gid", subgidFile, u.MapGID, u.MapCount, subgid); err != nil {
			return nil, nil, err
		}
	}
	if u.MapUID != self && !InRanges(u.MapUID, subuid) {
		return nil, nil, xerr.Permanentf("user: map_uid %d is not uid %d or in its subuid range", u.MapUID, self)
	}
	held := map[int]bool{hostenv.Getegid(): true}
	if gs, err := hostenv.Getgroups(); err == nil {
		for _, g := range gs {
			held[g] = true
		}
	}
	ownsGID := func(g int) bool { return held[g] || InRanges(g, subgid) }
	if !ownsGID(u.MapGID) {
		return nil, nil, xerr.Permanentf("user: map_gid %d is not held by the caller or in its subgid range", u.MapGID)
	}
	uidArgs = []string{strconv.Itoa(u.UID), strconv.Itoa(u.MapUID), "1"}

	// inside gid -> outside gid
	gmap := map[int]int{u.GID: u.MapGID}
	for _, g := range u.Groups {
		if g < 0 {
			return nil, nil, xerr.Permanentf("user: negative group %d", g)
		}
		if out, ok := gmap[g]; ok {
			if out != g {
				return nil, nil, xerr.Permanentf("user: group %d is the primary gid, which maps to %d", g, out)
			}
			continue
		}
		if !ownsGID(g) {
			return nil, nil, xerr.Permanentf("user: group %d is not held by the caller or in its subgid range", g)
		}
		gmap[g] = g
	}
	inside := make([]int, 0, len(gmap))
	for g := range gmap {
		inside = append(inside, g)
	}
	slices.Sort(inside)
	outs := map[int]bool{}
	for _, g := range inside {
		if outs[gmap[g]] {
			return nil, nil, xerr.Permanentf("user: gid %d mapped twice", gmap[g])
		}
		outs[gmap[g]] = true
		gidArgs = append(gidArgs, strconv.Itoa(g), strconv.Itoa(gmap[g]), "1")
	}
	return uidArgs, gidArgs, nil
}

// Check validates u for the current process and checks newuidmap and
// newgidmap are on PATH.
func Check(u User) error {
	self := os.Geteuid()
	subuid, err := ReadSubIDs(hostenv.SubUIDPath, self)
	if err != nil {
		return xerr.Permanentf("user: %s: %w", hostenv.SubUIDPath, err)
	}
	subgid, err := ReadSubIDs(hostenv.SubGIDPath, self)
	if err != nil {
		return xerr.Permanentf("user: %s: %w", hostenv.SubGIDPath, err)
	}
	if _, _, err := Maps(u, self, hostenv.SubUIDPath, hostenv.SubGIDPath, subuid, subgid); err != nil {
		return err
	}
	for _, c := range []string{"newuidmap", "newgidmap"} {
		if err := trace.LookCommand(c, nil); err != nil {
			return xerr.Permanentf("user: %s: %w", c, err)
		}
	}
	return nil
}
