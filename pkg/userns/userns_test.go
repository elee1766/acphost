package userns

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/elee1766/acphost/pkg/hostenv"
	"github.com/elee1766/acphost/pkg/xerr"
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
	oldE, oldG := hostenv.Getegid, hostenv.Getgroups
	t.Cleanup(func() { hostenv.Getegid, hostenv.Getgroups = oldE, oldG })
	hostenv.Getegid = func() int { return 1000 }
	hostenv.Getgroups = func() ([]int, error) { return []int{1000, 27, 100}, nil }

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
				u, g, err := Maps(tt.u, 1000, "subuid", "subgid", su, sg)
				if tt.wantErr {
					var pe *xerr.PermanentError
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

func TestUserMapsSpan(t *testing.T) {
	sub := []Range{{100000, 1000}, {101000, 1000}}
	tests := []struct {
		name             string
		mapUID, mapGID   int
		count            int
		wantErr, inError string
	}{
		{name: "inside one", mapUID: 100000, mapGID: 101000, count: 1000},
		{name: "count one ignores span", mapUID: 100999, mapGID: 100999, count: 1},
		{name: "spans adjacent", mapUID: 100500, mapGID: 100000, count: 1000, wantErr: "uids 100500..101499", inError: "subuid"},
		{name: "partly outside", mapUID: 101500, mapGID: 100000, count: 1000, wantErr: "uids 101500..102499", inError: "101000..101999"},
		{name: "gid only", mapUID: 100000, mapGID: 100900, count: 200, wantErr: "gids 100900..101099", inError: "subgid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := User{MapUID: tt.mapUID, MapGID: tt.mapGID, MapCount: tt.count}
			_, _, err := Maps(u, 1000, "subuid", "subgid", sub, sub)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var pe *xerr.PermanentError
			if !errors.As(err, &pe) || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), tt.inError) {
				t.Fatalf("want PermanentError with %q and %q, got %v", tt.wantErr, tt.inError, err)
			}
		})
	}
}
