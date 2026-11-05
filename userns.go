package acphost

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
)

// User on a Spec runs the agent as UID:GID inside a user
// namespace. The inside ids map to MapUID and MapGID outside, which the
// caller must own: its own id, a gid it holds, or an id in its
// /etc/subuid or /etc/subgid range. Groups are supplementary gids, each
// mapped to the same gid outside.
type User struct {
	UID    int   `json:"uid"`
	GID    int   `json:"gid"`
	MapUID int   `json:"map_uid"`
	MapGID int   `json:"map_gid"`
	Groups []int `json:"groups,omitempty"`
}

// Range is one subordinate id range from /etc/subuid or /etc/subgid.
type Range struct {
	Start, Count int
}

func (r Range) contains(id int) bool { return id >= r.Start && id-r.Start < r.Count }

// Paths of the subordinate id files. Variables so tests can point them
// elsewhere.
var (
	subuidPath = "/etc/subuid"
	subgidPath = "/etc/subgid"
)

// Caller id lookups, replaced in tests.
var (
	getegid   = os.Getegid
	getgroups = os.Getgroups
)

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

// readSubIDs parses path for uid. A missing file means no ranges.
func readSubIDs(path string, uid int) ([]Range, error) {
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

func inRanges(id int, rs []Range) bool {
	return slices.ContainsFunc(rs, func(r Range) bool { return r.contains(id) })
}

// userMaps builds the id map arguments for newuidmap and newgidmap (the
// part after the pid: inside outside count, repeated). self is the
// caller's uid. Ids the caller cannot map are a PermanentError. Output is
// sorted so the same User always gives the same arguments.
func userMaps(u User, self int, subuid, subgid []Range) (uidArgs, gidArgs []string, err error) {
	if u.UID < 0 || u.GID < 0 || u.MapUID < 0 || u.MapGID < 0 {
		return nil, nil, Permanentf("user: negative id")
	}
	if u.MapUID != self && !inRanges(u.MapUID, subuid) {
		return nil, nil, Permanentf("user: map_uid %d is not uid %d or in its subuid range", u.MapUID, self)
	}
	held := map[int]bool{getegid(): true}
	if gs, err := getgroups(); err == nil {
		for _, g := range gs {
			held[g] = true
		}
	}
	ownsGID := func(g int) bool { return held[g] || inRanges(g, subgid) }
	if !ownsGID(u.MapGID) {
		return nil, nil, Permanentf("user: map_gid %d is not held by the caller or in its subgid range", u.MapGID)
	}
	uidArgs = []string{strconv.Itoa(u.UID), strconv.Itoa(u.MapUID), "1"}

	// inside gid -> outside gid
	gmap := map[int]int{u.GID: u.MapGID}
	for _, g := range u.Groups {
		if g < 0 {
			return nil, nil, Permanentf("user: negative group %d", g)
		}
		if out, ok := gmap[g]; ok {
			if out != g {
				return nil, nil, Permanentf("user: group %d is the primary gid, which maps to %d", g, out)
			}
			continue
		}
		if !ownsGID(g) {
			return nil, nil, Permanentf("user: group %d is not held by the caller or in its subgid range", g)
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
			return nil, nil, Permanentf("user: gid %d mapped twice", gmap[g])
		}
		outs[gmap[g]] = true
		gidArgs = append(gidArgs, strconv.Itoa(g), strconv.Itoa(gmap[g]), "1")
	}
	return uidArgs, gidArgs, nil
}

// checkUser validates u for the current process and checks newuidmap and
// newgidmap are on PATH.
func checkUser(u User) error {
	self := os.Geteuid()
	subuid, err := readSubIDs(subuidPath, self)
	if err != nil {
		return Permanentf("user: %s: %w", subuidPath, err)
	}
	subgid, err := readSubIDs(subgidPath, self)
	if err != nil {
		return Permanentf("user: %s: %w", subgidPath, err)
	}
	if _, _, err := userMaps(u, self, subuid, subgid); err != nil {
		return err
	}
	for _, c := range []string{"newuidmap", "newgidmap"} {
		if err := lookCommand(c, nil); err != nil {
			return Permanentf("user: %s: %w", c, err)
		}
	}
	return nil
}
