package acphost

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dnsFixture is the all-OK fixture with a fake dns check that fails
// with err (nil for success) and counts its calls.
func dnsFixture(t *testing.T, name string, err error) (preflightFixture, *int) {
	f := newPreflightFixture(t)
	calls := new(int)
	f.env.dnsCheck = func(Sandbox, string) error { *calls++; return err }
	if name != "" {
		WithDNSCheck(name)(&f.env)
	}
	return f, calls
}

func TestPreflightDNSOff(t *testing.T) {
	f, calls := dnsFixture(t, "", errors.New("boom"))
	r, _ := preflight(f.spec(), f.env)
	if *calls != 0 || len(r.Checks) != len(allChecks) || !r.OK() {
		t.Fatalf("calls=%d report = %+v", *calls, r)
	}
}

func TestPreflightDNSNoPasta(t *testing.T) {
	f, calls := dnsFixture(t, "example.com", errors.New("boom"))
	if err := os.Remove(filepath.Join(f.env.path, "pasta")); err != nil {
		t.Fatal(err)
	}
	r, _ := preflight(f.spec(), f.env)
	for _, c := range r.Checks {
		if c.Name == "sandbox.dns" {
			t.Fatalf("dns check ran without pasta: %+v", r)
		}
	}
	if *calls != 0 {
		t.Fatalf("calls = %d", *calls)
	}
}

func TestPreflightDNSNotPastaNetwork(t *testing.T) {
	f, calls := dnsFixture(t, "example.com", errors.New("boom"))
	r, _ := preflight(Isolated{Sandbox: &Sandbox{Network: "none"}}, f.env)
	if *calls != 0 || len(r.Checks) != 1 {
		t.Fatalf("calls=%d report = %+v", *calls, r)
	}
}

func TestPreflightDNSFails(t *testing.T) {
	f, calls := dnsFixture(t, "example.com", errors.New("Temporary failure in name resolution"))
	r, _ := preflight(f.spec(), f.env)
	last := r.Checks[len(r.Checks)-1]
	if *calls != 1 || last.Name != "sandbox.dns" || last.OK || r.OK() {
		t.Fatalf("calls=%d report = %+v", *calls, r)
	}
	if !strings.Contains(last.Reason, "example.com") || !strings.Contains(last.Hint, "sandbox.dns.forward") {
		t.Fatalf("check = %+v", last)
	}
}

func TestPreflightDNSOK(t *testing.T) {
	f, _ := dnsFixture(t, "example.com", nil)
	r, _ := preflight(f.spec(), f.env)
	if last := r.Checks[len(r.Checks)-1]; last.Name != "sandbox.dns" || !r.OK() {
		t.Fatalf("report = %+v", r)
	}
}
