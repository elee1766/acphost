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
	trace("cgroupfs:kill")
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

// eventsValue reads one "key value" line from a cgroup events file, 0 if
// missing.
func eventsValue(path, key string) int64 {
	trace("cgroupfs:events")
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

// CgroupStats is the payload of MethodCgroupStats and Exit.Cgroup: usage
// of the session cgroup when read. Values are the kernel's own counters,
// not deltas.
type CgroupStats struct {
	MemoryCurrent   int64 `json:"memory_current"`    // memory.current, bytes
	MemoryPeak      int64 `json:"memory_peak"`       // memory.peak, bytes; 0 where the kernel lacks it
	CPUUsageUsec    int64 `json:"cpu_usage_usec"`    // cpu.stat usage_usec
	OOMKills        int64 `json:"oom_kills"`         // memory.events oom_kill
	MemoryMaxEvents int64 `json:"memory_max_events"` // memory.events max: times usage hit memory.max
}

// readCgroupStats reads usage from the cgroup dir. A missing file leaves
// its field 0.
func readCgroupStats(dir string) CgroupStats {
	ev := filepath.Join(dir, "memory.events")
	return CgroupStats{
		MemoryCurrent:   singleValue(filepath.Join(dir, "memory.current")),
		MemoryPeak:      singleValue(filepath.Join(dir, "memory.peak")),
		CPUUsageUsec:    eventsValue(filepath.Join(dir, "cpu.stat"), "usage_usec"),
		OOMKills:        eventsValue(ev, "oom_kill"),
		MemoryMaxEvents: eventsValue(ev, "max"),
	}
}

// singleValue reads a cgroup file holding one integer, 0 if missing.
func singleValue(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}
