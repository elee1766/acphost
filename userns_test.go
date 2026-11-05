package acphost

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseSubIDs(t *testing.T) {
	in := "# comment\n\nalice:100000:65536\n1000:200000:10\nbob:300000:5\n"
	got, err := ParseSubIDs(strings.NewReader(in), "alice", 1000)
	if err != nil {
		t.Fatal(err)
	}
	want := []Range{{100000, 65536}, {200000, 10}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	got, err = ParseSubIDs(strings.NewReader(in), "carol", 1001)
	if err != nil || len(got) != 0 {
		t.Fatalf("missing entry: got %v %v", got, err)
	}
	if _, err := ParseSubIDs(strings.NewReader("alice:x:1\n"), "alice", 1); err == nil {
		t.Fatal("want error for bad line")
	}
}

func TestUserMaps(t *testing.T) {
	oldE, oldG := getegid, getgroups
	t.Cleanup(func() { getegid, getgroups = oldE, oldG })
	getegid = func() int { return 1000 }
	getgroups = func() ([]int, error) { return []int{1000, 27, 100}, nil }

	subuid := []Range{{100000, 65536}}
	subgid := []Range{{100000, 65536}}
	tests := []struct {
		name     string
		u        User
		sub      bool // use sub ranges
		uid, gid string
		wantErr  bool
	}{
		{name: "self", u: User{UID: 0, GID: 0, MapUID: 1000, MapGID: 1000}, uid: "0 1000 1", gid: "0 1000 1"},
		{name: "in range", u: User{UID: 1000, GID: 1000, MapUID: 100500, MapGID: 100500}, sub: true, uid: "1000 100500 1", gid: "1000 100500 1"},
		{name: "uid out of range", u: User{MapUID: 99999, MapGID: 1000}, sub: true, wantErr: true},
		{name: "gid out of range", u: User{MapUID: 1000, MapGID: 99999}, sub: true, wantErr: true},
		{name: "missing entry", u: User{MapUID: 100500, MapGID: 1000}, wantErr: true},
		{
			name: "groups allowed sorted", u: User{UID: 0, GID: 0, MapUID: 1000, MapGID: 1000, Groups: []int{100, 27, 100200, 27}}, sub: true,
			uid: "0 1000 1", gid: "0 1000 1 27 27 1 100 100 1 100200 100200 1",
		},
		{name: "group rejected", u: User{MapUID: 1000, MapGID: 1000, Groups: []int{5}}, sub: true, wantErr: true},
		{name: "group collides with map_gid", u: User{UID: 0, GID: 0, MapUID: 1000, MapGID: 1000, Groups: []int{1000}}, wantErr: true},
		{name: "negative", u: User{UID: -1, MapUID: 1000, MapGID: 1000}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var su, sg []Range
			if tt.sub {
				su, sg = subuid, subgid
			}
			for range 3 { // deterministic across runs
				u, g, err := userMaps(tt.u, 1000, su, sg)
				if tt.wantErr {
					var pe *PermanentError
					if !errors.As(err, &pe) {
						t.Fatalf("want PermanentError, got %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if s := strings.Join(u, " "); s != tt.uid {
					t.Fatalf("uid args %q want %q", s, tt.uid)
				}
				if s := strings.Join(g, " "); s != tt.gid {
					t.Fatalf("gid args %q want %q", s, tt.gid)
				}
			}
		})
	}
}

func TestCheckSpawnUserBadSubuid(t *testing.T) {
	dir := t.TempDir()
	oldU, oldG := subuidPath, subgidPath
	t.Cleanup(func() { subuidPath, subgidPath = oldU, oldG })
	subuidPath = filepath.Join(dir, "subuid")
	subgidPath = filepath.Join(dir, "subgid")
	if err := os.WriteFile(subuidPath, []byte("bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := Spec{Command: "true", Dir: dir, User: &User{MapUID: os.Geteuid(), MapGID: os.Getegid()}}
	var pe *PermanentError
	if err := CheckSpawn(s, false); !errors.As(err, &pe) {
		t.Fatalf("want PermanentError for bad subuid, got %v", err)
	}
}
