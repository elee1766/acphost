package acphost

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// Follow intervals. With inotify the poll is only a safety net for a
// missed event; without it the follower polls as it always did.
var (
	followSafetyPoll = 5 * time.Second
	followFallback   = 200 * time.Millisecond
	// followInotify is false in tests that force the poll fallback.
	followInotify = true
)

// dirWatch wakes on any write, create, rename or delete in one directory.
// The journal and its compressed part live there, so a wake on any of
// them is enough: the follower rereads from its offset either way.
type dirWatch struct {
	f    *os.File
	wake chan struct{}
}

func newDirWatch(dir string) (*dirWatch, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}
	const mask = unix.IN_MODIFY | unix.IN_CLOSE_WRITE | unix.IN_CREATE |
		unix.IN_MOVED_TO | unix.IN_MOVED_FROM | unix.IN_DELETE
	if _, err := unix.InotifyAddWatch(fd, dir, mask); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	// A nonblocking fd in an os.File goes through the runtime poller, so
	// an idle follower parks with no thread and Close unblocks Read.
	w := &dirWatch{f: os.NewFile(uintptr(fd), "inotify"), wake: make(chan struct{}, 1)}
	go w.loop()
	return w, nil
}

// loop ends when Close makes Read fail.
func (w *dirWatch) loop() {
	buf := make([]byte, 4096)
	for {
		n, err := w.f.Read(buf)
		if err != nil {
			return
		}
		if n > 0 {
			select {
			case w.wake <- struct{}{}:
			default:
			}
		}
	}
}

func (w *dirWatch) Close() { _ = w.f.Close() }

// followWaiter returns a channel that fires when the journal may have
// grown, and a stop func. It falls back to the plain poll if inotify
// cannot be set up.
func followWaiter(path string) (wait func() <-chan time.Time, wake <-chan struct{}, stop func()) {
	if followInotify {
		if w, err := newDirWatch(filepath.Dir(path)); err == nil {
			return func() <-chan time.Time { return time.After(followSafetyPoll) }, w.wake, w.Close
		}
	}
	return func() <-chan time.Time { return time.After(followFallback) }, nil, func() {}
}

// FollowJournal calls fn for each frame appended to path after byte offset
// off until ctx ends. It wakes on inotify events in the journal's
// directory, with a slow poll as a safety net, or polls every 200 ms if
// inotify is unavailable.
func FollowJournal(ctx context.Context, path string, off int64, fn func(Frame)) error {
	tick, wake, stop := followWaiter(path)
	defer stop()
	for {
		var err error
		off, err = readFrom(path, off, fn)
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
		case <-tick():
		}
	}
}
