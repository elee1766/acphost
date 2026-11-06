package acphost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// followOnce starts a follower on an empty journal, appends one line, and
// returns how long it took to arrive.
func followOnce(t *testing.T) time.Duration {
	t.Helper()
	path := filepath.Join(t.TempDir(), JournalFile)
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	ctx, cancel := context.WithCancel(t.Context())
	got := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = FollowJournal(ctx, path, 0, func(Frame) { got <- struct{}{} })
	}()
	// Wait for the follower to exit before the caller's cleanup resets knobs.
	defer func() { cancel(); <-done }()
	time.Sleep(50 * time.Millisecond) // let the first read and watch run
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	t0 := time.Now()
	_, _ = f.WriteString(`{"seq":1,"msg":{}}` + "\n")
	_ = f.Close()
	select {
	case <-got:
		return time.Since(t0)
	case <-time.After(10 * time.Second):
		t.Fatal("no frame followed")
		return 0
	}
}

func TestFollowJournalInotifyLatency(t *testing.T) {
	old := followSafetyPoll
	followSafetyPoll = time.Hour // only inotify can deliver in time
	t.Cleanup(func() { followSafetyPoll = old })
	if d := followOnce(t); d > 100*time.Millisecond {
		t.Fatalf("frame took %v, want well under 200ms", d)
	}
}

func TestFollowJournalPollFallback(t *testing.T) {
	followInotify = false
	t.Cleanup(func() { followInotify = true })
	followOnce(t)
}

func TestFollowJournalRename(t *testing.T) {
	// A rename (the journal replaced) wakes the follower too.
	old := followSafetyPoll
	followSafetyPoll = time.Hour
	t.Cleanup(func() { followSafetyPoll = old })
	dir := t.TempDir()
	path := filepath.Join(dir, JournalFile)
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	ctx, cancel := context.WithCancel(t.Context())
	got := make(chan int64, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = FollowJournal(ctx, path, 0, func(f Frame) { got <- f.Seq })
	}()
	defer func() { cancel(); <-done }()
	time.Sleep(50 * time.Millisecond)
	tmp := filepath.Join(dir, "tmp")
	require.NoError(t, os.WriteFile(tmp, []byte(`{"seq":7,"msg":{}}`+"\n"), 0o600))
	require.NoError(t, os.Rename(tmp, path))
	select {
	case s := <-got:
		require.Equal(t, int64(7), s)
	case <-time.After(time.Second):
		t.Fatal("rename not followed")
	}
}

// BenchmarkFollowIdle reports CPU time used by 50 idle followers over one
// second, and the latency of one appended frame. Run with
// -run '^$' -bench FollowIdle -benchtime 1x, once per mode.
func BenchmarkFollowIdle(b *testing.B) {
	for _, mode := range []string{"inotify", "poll"} {
		b.Run(mode, func(b *testing.B) {
			followInotify = mode == "inotify"
			defer func() { followInotify = true }()
			var wg sync.WaitGroup
			for range b.N {
				dir := b.TempDir()
				ctx, cancel := context.WithCancel(context.Background())
				got := make(chan struct{}, 64)
				var paths []string
				for i := range 50 {
					p := filepath.Join(dir, fmt.Sprint(i), JournalFile)
					_ = os.MkdirAll(filepath.Dir(p), 0o700)
					_ = os.WriteFile(p, nil, 0o600)
					paths = append(paths, p)
					wg.Go(func() { _ = FollowJournal(ctx, p, 0, func(Frame) { got <- struct{}{} }) })
				}
				time.Sleep(100 * time.Millisecond)
				r0 := cpu()
				time.Sleep(time.Second)
				idle := cpu() - r0
				t0 := time.Now()
				_ = os.WriteFile(paths[0], []byte(`{"seq":1,"msg":{}}`+"\n"), 0o600)
				<-got
				lat := time.Since(t0)
				cancel()
				wg.Wait()
				b.ReportMetric(float64(idle.Microseconds()), "cpu-us/s")
				b.ReportMetric(float64(lat.Microseconds()), "latency-us")
			}
		})
	}
}

func cpu() time.Duration {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}
