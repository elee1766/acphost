package acphost

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// cgroupKill writes 1 to dir/cgroup.kill, which SIGKILLs every process in
// the cgroup, including ones that left the agent's process group. It
// reports false if the kernel has no cgroup.kill (before 5.14).
func cgroupKill(dir string) bool {
	return os.WriteFile(filepath.Join(dir, "cgroup.kill"), []byte("1"), 0) == nil
}

// cgroupPopulated reads the populated key of dir/cgroup.events. A read
// error counts as empty so a vanished cgroup does not hold the host.
func cgroupPopulated(dir string) bool {
	return eventsValue(filepath.Join(dir, "cgroup.events"), "populated") > 0
}

// waitCgroupEmpty polls cgroup.events until populated is 0 or timeout.
// cgroupfs supports inotify on it, but polling keeps this simple and the
// wait is short: everything has been SIGKILLed by then.
func waitCgroupEmpty(dir string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for cgroupPopulated(dir) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	return true
}

// cgroupOOMKills returns the oom_kill count from dir/memory.events.
func cgroupOOMKills(dir string) int64 {
	return eventsValue(filepath.Join(dir, "memory.events"), "oom_kill")
}

// eventsValue reads one "key value" line from a cgroup events file, 0 if
// missing.
func eventsValue(path, key string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, key+" "); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n
		}
	}
	return 0
}
