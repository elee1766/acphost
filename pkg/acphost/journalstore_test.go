package acphost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeJournal writes n frames to dir's journal, with a big frame every
// bigEvery frames (0 for none), and returns its path.
func writeJournal(t testing.TB, dir string, n, bigEvery int) string {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		text := fmt.Sprintf("chunk %d", i)
		if bigEvery > 0 && i%bigEvery == 0 {
			text = strings.Repeat("x", maxBlock+1000)
		}
		fmt.Fprintf(&b, `{"seq":%d,"t":%d,"from":"agent","msg":{"jsonrpc":"2.0","method":"session/update","params":{"text":%q}}}`+"\n", i, 1000+i, text)
	}
	p := filepath.Join(dir, JournalFile)
	require.NoError(t, os.WriteFile(p, []byte(b.String()), 0o600))
	return p
}

// setMaxBlock lowers maxBlock for one test.
func setMaxBlock(t *testing.T, n int) {
	old := maxBlock
	maxBlock = n
	t.Cleanup(func() { maxBlock = old })
}

type readCase struct {
	name  string
	since int64
}

var readCases = []readCase{{"all", 0}, {"since1", 1}, {"since300", 300}, {"since-mid", 517}, {"tail1", -1}, {"tail10", -10}, {"tail300", -300}, {"past", 5000}}

func readAll(t *testing.T, p string) map[string][]Frame {
	t.Helper()
	out := map[string][]Frame{}
	for _, c := range readCases {
		fr, _, err := ReadJournal(p, c.since)
		require.NoError(t, err, c.name)
		out[c.name] = fr
	}
	fr, off, err := ReadFrom(p, 0)
	require.NoError(t, err)
	out["from0"] = fr
	if len(fr) > 5 {
		// ReadFrom at the offset ReadJournal reports after a partial read.
		_, mid, err := ReadJournal(p, -1)
		require.NoError(t, err)
		assert.Equal(t, off, mid)
	}
	return out
}

func TestCompressRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name        string
		n, bigEvery int
		level       int
	}{{"small", 13, 0, 0}, {"blocks", 1000, 0, 19}, {"bigframes", 600, 200, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bigEvery > 0 {
				// Frames over maxBlock with a small block cap: same
				// paths, a few KB instead of 12 MB under -race.
				setMaxBlock(t, 4<<10)
			}
			plainDir, dir := t.TempDir(), t.TempDir()
			pp := writeJournal(t, plainDir, tc.n, tc.bigEvery)
			p := writeJournal(t, dir, tc.n, tc.bigEvery)
			want := readAll(t, pp)
			wantSeq, wantT := JournalTail(plainDir)
			assert.Equal(t, int64(tc.n), wantSeq)

			require.NoError(t, CompressJournal(dir, tc.level))
			_, err := os.Stat(p)
			assert.ErrorIs(t, err, os.ErrNotExist)
			_, err = os.Stat(filepath.Join(dir, CompressedJournalFile))
			require.NoError(t, err)

			assert.Equal(t, want, readAll(t, p))
			seq, tm := JournalTail(dir)
			assert.Equal(t, wantSeq, seq)
			assert.Equal(t, wantT, tm)

			// Every index record points at the start of its frame.
			s, err := openSource(p)
			require.NoError(t, err)
			defer func() { _ = s.Close() }()
			recs, ok, err := readIndex(idxPath(p), s.size)
			require.NoError(t, err)
			require.True(t, ok)
			for _, r := range recs {
				var got []Frame
				_, err := s.scanFramesAt(r.off, func(fr Frame, _ int64) { got = append(got, fr) })
				require.NoError(t, err)
				require.NotEmpty(t, got)
				assert.Equal(t, r.seq, got[0].Seq)
			}
			// Again is a no-op.
			require.NoError(t, CompressJournal(dir, tc.level))
		})
	}
}

func TestCompressLevel(t *testing.T) {
	dir := t.TempDir()
	writeJournal(t, dir, 3, 0)
	assert.Error(t, CompressJournal(dir, 23))
	assert.Error(t, CompressJournal(dir, -1))
}

func TestCompressCrash(t *testing.T) {
	dir := t.TempDir()
	p := writeJournal(t, dir, 700, 0)
	want := readAll(t, p)

	// A crash while writing the .zst.tmp: readers use the plain file, and
	// the next compress removes the tmp and starts over.
	tmp := filepath.Join(dir, CompressedJournalFile+".tmp")
	require.NoError(t, os.WriteFile(tmp, []byte("partial"), 0o600))
	assert.Equal(t, want, readAll(t, p))
	require.NoError(t, CompressJournal(dir, 3))
	_, err := os.Stat(tmp)
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.Equal(t, want, readAll(t, p))

	// A crash after the rename, before the plain file was removed: both
	// exist, readers prefer the plain file, the next compress removes it.
	dir2 := t.TempDir()
	p2 := writeJournal(t, dir2, 700, 0)
	zb, err := os.ReadFile(filepath.Join(dir, CompressedJournalFile))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir2, CompressedJournalFile), zb, 0o600))
	assert.Equal(t, want, readAll(t, p2))
	require.NoError(t, CompressJournal(dir2, 3))
	_, err = os.Stat(p2)
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.Equal(t, want, readAll(t, p2))
}

func TestIndexRebuild(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		t.Run(fmt.Sprint("compressed=", compressed), func(t *testing.T) {
			dir := t.TempDir()
			p := writeJournal(t, dir, 900, 0)
			want := readAll(t, p)
			if compressed {
				require.NoError(t, CompressJournal(dir, 3))
			}
			ip := idxPath(p)

			// Missing.
			require.NoError(t, os.Remove(ip))
			assert.Equal(t, want, readAll(t, p))
			full, err := os.ReadFile(ip)
			require.NoError(t, err)
			assert.NotEmpty(t, full)

			// Corrupt: a torn record and garbage.
			require.NoError(t, os.WriteFile(ip, append(append([]byte{}, full[:20]...), 0xff, 0xff), 0o600))
			assert.Equal(t, want, readAll(t, p))
			got, err := os.ReadFile(ip)
			require.NoError(t, err)
			assert.Equal(t, full, got)
			require.NoError(t, os.WriteFile(ip, []byte(strings.Repeat("\xff", 32)), 0o600))
			assert.Equal(t, want, readAll(t, p))
		})
	}
}

func TestIndexBehind(t *testing.T) {
	dir := t.TempDir()
	p := writeJournal(t, dir, 900, 0)
	// Index the first 600 frames only, as if the host crashed then.
	short := t.TempDir()
	writeJournal(t, short, 600, 0)
	seq, _ := JournalTail(short)
	require.Equal(t, int64(600), seq)
	_, _, err := ReadJournal(filepath.Join(short, JournalFile), 1)
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(short, IndexFile))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(idxPath(p), b, 0o600))

	seq, tm := JournalTail(dir)
	assert.Equal(t, int64(900), seq)
	assert.Equal(t, time.UnixMilli(1900), tm)
	fr, _, err := ReadJournal(p, -3)
	require.NoError(t, err)
	require.Len(t, fr, 3)
	assert.Equal(t, int64(898), fr[0].Seq)
	fr, _, err = ReadJournal(p, 700)
	require.NoError(t, err)
	assert.Len(t, fr, 200)

	s, err := openSource(p)
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	recs, ok, err := readIndex(idxPath(p), s.size)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, int64(900), recs[len(recs)-1].seq)
}

func TestTailNoIndex(t *testing.T) {
	dir := t.TempDir()
	p := writeJournal(t, dir, 50, 20)
	// A half-written last line is ignored.
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString(`{"seq":51,"t":`)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	seq, tm := JournalTail(dir)
	assert.Equal(t, int64(50), seq)
	assert.Equal(t, time.UnixMilli(1050), tm)
	_, err = os.Stat(filepath.Join(dir, IndexFile))
	assert.ErrorIs(t, err, os.ErrNotExist)

	seq, _ = JournalTail(t.TempDir())
	assert.Zero(t, seq)
}

func TestJournalTailCases(t *testing.T) {
	line := func(seq int, text string) string {
		return fmt.Sprintf(`{"seq":%d,"t":%d,"from":"agent","msg":{"text":%q}}`+"\n", seq, 1000+seq, text)
	}
	big := strings.Repeat("z", 200<<10)
	cases := []struct {
		name    string
		body    string
		wantSeq int64
	}{
		{"empty", "", 0},
		{"single", line(1, "a"), 1},
		{"big-last", line(1, "a") + line(2, "b") + line(3, big), 3},
		{"only-big", line(7, big), 7},
		{"partial", line(1, "a") + line(2, "b") + `{"seq":3,"t":`, 2},
		{"partial-big", line(1, "a") + line(2, big) + `{"seq":3,"t":` + big, 2},
	}
	for _, c := range cases {
		for _, compressed := range []bool{false, true} {
			t.Run(fmt.Sprint(c.name, "/compressed=", compressed), func(t *testing.T) {
				setMaxBlock(t, 1000)
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, JournalFile), []byte(c.body), 0o600))
				if compressed {
					require.NoError(t, CompressJournal(dir, 3))
					// The seek table alone answers: drop the index.
					require.NoError(t, os.Remove(filepath.Join(dir, IndexFile)))
				}
				seq, tm := JournalTail(dir)
				assert.Equal(t, c.wantSeq, seq)
				if c.wantSeq > 0 {
					assert.Equal(t, time.UnixMilli(1000+c.wantSeq), tm)
				} else {
					assert.True(t, tm.IsZero())
				}
				if !compressed {
					_, err := os.Stat(filepath.Join(dir, IndexFile))
					assert.ErrorIs(t, err, os.ErrNotExist)
				}
			})
		}
	}
}

func TestHostRefusesCompressed(t *testing.T) {
	dir := t.TempDir()
	writeJournal(t, dir, 3, 0)
	require.NoError(t, CompressJournal(dir, 3))
	require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol, Session: "s", Command: "true", Dir: dir}))
	assert.ErrorIs(t, Main(dir), ErrJournalCompressed)
}

// BenchmarkJournalTail: the tail of a ~100 MB journal costs one 64 KiB
// read (plain) or one block (compressed), not the file.
func BenchmarkJournalTail(b *testing.B) {
	for _, compressed := range []bool{false, true} {
		b.Run(fmt.Sprint("compressed=", compressed), func(b *testing.B) {
			dir := b.TempDir()
			var sb strings.Builder
			pad := strings.Repeat("y", 300)
			for i := 1; sb.Len() < 100<<20; i++ {
				fmt.Fprintf(&sb, `{"seq":%d,"t":%d,"from":"agent","msg":{"text":"%s %d"}}`+"\n", i, i, pad, i)
			}
			require.NoError(b, os.WriteFile(filepath.Join(dir, JournalFile), []byte(sb.String()), 0o600))
			if compressed {
				require.NoError(b, CompressJournal(dir, 3))
			}
			JournalTail(dir)
			b.ResetTimer()
			for range b.N {
				if seq, _ := JournalTail(dir); seq == 0 {
					b.Fatal("no tail")
				}
			}
		})
	}
}
