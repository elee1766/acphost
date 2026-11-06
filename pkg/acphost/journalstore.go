package acphost

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Journal storage: the plain journal.ndjson while a host runs, optionally
// journal.ndjson.zst once it has finished, and journal.idx next to either.
//
// journal.idx is a sequence of 16-byte little-endian records (seq, offset):
// frame seq starts at byte offset in the uncompressed journal. Records are
// written every IndexEvery frames and for the last frame, so a reader can
// seek instead of scanning the whole file.
//
// journal.ndjson.zst is one zstd frame per block of lines followed by a
// seek table in a skippable frame, as in the zstd seekable format
// (contrib/seekable_format in the zstd repo). Plain zstd tools decompress
// it as usual.
const (
	CompressedJournalFile = JournalFile + ".zst"
	IndexFile             = "journal.idx"

	// IndexEvery is the number of frames between index records.
	IndexEvery = 256

	idxRecSize = 16

	skippableMagic = 0x184D2A5E
	seekableMagic  = 0x8F92EAB1
)

// maxBlock caps the uncompressed size of one zstd block, so a seek
// decompresses at most about this much (or one frame, if larger). A
// variable so tests can cross it without writing megabytes.
var maxBlock = 4 << 20

// ErrJournalCompressed is returned when a host is started in a session
// whose journal has been compressed. A compressed journal is final.
var ErrJournalCompressed = errors.New("acphost: journal is compressed, the session is closed")

type idxRec struct{ seq, off int64 }

func appendIdx(b []byte, r idxRec) []byte {
	b = binary.LittleEndian.AppendUint64(b, uint64(r.seq))
	return binary.LittleEndian.AppendUint64(b, uint64(r.off))
}

func idxPath(jpath string) string { return filepath.Join(filepath.Dir(jpath), IndexFile) }

// journalSource is the readable form of a journal: the plain file if it
// exists (it wins over a .zst left by a crash mid-compress), else the .zst.
type journalSource struct {
	f    *os.File
	zst  bool
	size int64 // uncompressed size, -1 if unknown
	seek []zframe
	data int64 // end of the zstd data frames in a .zst (start of the seek table)
}

type zframe struct{ coff, doff, dsize int64 }

func openSource(jpath string) (*journalSource, error) {
	f, err := os.Open(jpath)
	if err == nil {
		st, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		return &journalSource{f: f, size: st.Size()}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	zf, zerr := os.Open(jpath + ".zst")
	if zerr != nil {
		return nil, err // report the plain file as missing
	}
	s := &journalSource{f: zf, zst: true, size: -1}
	if st, err := zf.Stat(); err == nil {
		s.data = st.Size()
		s.readSeekTable(st.Size())
	}
	return s, nil
}

func (s *journalSource) Close() error { return s.f.Close() }

// readSeekTable loads the seek table. Without one the .zst is still read,
// from the start.
func (s *journalSource) readSeekTable(fsize int64) {
	var foot [9]byte
	if fsize < 8+9 {
		return
	}
	if _, err := s.f.ReadAt(foot[:], fsize-9); err != nil {
		return
	}
	if binary.LittleEndian.Uint32(foot[5:]) != seekableMagic {
		return
	}
	n := int64(binary.LittleEndian.Uint32(foot[:4]))
	tsize := n*8 + 9
	start := fsize - tsize - 8
	if start < 0 {
		return
	}
	buf := make([]byte, tsize+8)
	if _, err := s.f.ReadAt(buf, start); err != nil {
		return
	}
	if binary.LittleEndian.Uint32(buf) != skippableMagic || int64(binary.LittleEndian.Uint32(buf[4:])) != tsize {
		return
	}
	var coff, doff int64
	frames := make([]zframe, 0, n)
	for i := int64(0); i < n; i++ {
		e := buf[8+i*8:]
		c, d := int64(binary.LittleEndian.Uint32(e)), int64(binary.LittleEndian.Uint32(e[4:]))
		frames = append(frames, zframe{coff, doff, d})
		coff += c
		doff += d
	}
	if coff != start {
		return
	}
	s.seek, s.data, s.size = frames, start, doff
}

// openAt returns the uncompressed journal from byte offset off.
func (s *journalSource) openAt(off int64) (io.ReadCloser, error) {
	if !s.zst {
		if _, err := s.f.Seek(off, io.SeekStart); err != nil {
			return nil, err
		}
		return io.NopCloser(s.f), nil
	}
	var cstart, dstart int64
	for _, z := range s.seek {
		if z.doff > off {
			break
		}
		cstart, dstart = z.coff, z.doff
	}
	d, err := zstd.NewReader(io.NewSectionReader(s.f, cstart, s.data-cstart), zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	if _, err := io.CopyN(io.Discard, d, off-dstart); err != nil && !errors.Is(err, io.EOF) {
		d.Close()
		return nil, err
	}
	return zstdReadCloser{d}, nil
}

type zstdReadCloser struct{ *zstd.Decoder }

func (z zstdReadCloser) Close() error { z.Decoder.Close(); return nil }

// scanLines calls fn with each complete line from r and its start offset
// (r begins at off). It returns the offset after the last complete line.
func scanLines(r io.Reader, off int64, fn func(line []byte, start int64)) (int64, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return off, nil
			}
			return off, err
		}
		fn(line, off)
		off += int64(len(line))
	}
}

// scanFramesAt scans frames from uncompressed offset off.
func (s *journalSource) scanFramesAt(off int64, fn func(fr Frame, start int64)) (int64, error) {
	r, err := s.openAt(off)
	if err != nil {
		return off, err
	}
	defer func() { _ = r.Close() }()
	return scanLines(r, off, func(line []byte, start int64) {
		var fr Frame
		if json.Unmarshal(line, &fr) == nil {
			fn(fr, start)
		}
	})
}

// readIndex loads journal.idx. ok is false if the file had a broken
// record, which makes the caller rewrite it. Records not after the one
// before them (a duplicate from two writers) are skipped.
func readIndex(path string, size int64) (recs []idxRec, ok bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	ok = len(b)%idxRecSize == 0
	for i := 0; i+idxRecSize <= len(b); i += idxRecSize {
		r := idxRec{int64(binary.LittleEndian.Uint64(b[i:])), int64(binary.LittleEndian.Uint64(b[i+8:]))}
		if r.seq <= 0 || r.off < 0 || (size >= 0 && r.off >= size) {
			return recs, false, nil
		}
		if n := len(recs); n > 0 {
			p := recs[n-1]
			if r.seq <= p.seq && r.off <= p.off {
				continue
			}
			if r.seq <= p.seq || r.off <= p.off {
				return recs, false, nil
			}
		}
		recs = append(recs, r)
	}
	return recs, ok, nil
}

// journalIndex is the index of a journal plus its last frame.
type journalIndex struct {
	recs    []idxRec
	lastSeq int64
	lastT   int64
}

// seekSeq returns the offset to scan from to see every frame with seq >= seq.
func (ix *journalIndex) seekSeq(seq int64) int64 {
	var off int64
	for _, r := range ix.recs {
		if r.seq > seq {
			break
		}
		off = r.off
	}
	return off
}

// ensureIndex loads the index, extends it over frames past its last
// record, rebuilds it if missing or broken, and writes any change back
// (best effort: a read-only directory still gets a correct answer).
func ensureIndex(s *journalSource, jpath string) (*journalIndex, error) {
	ipath := idxPath(jpath)
	recs, ok, err := readIndex(ipath, s.size)
	if err != nil {
		recs, ok = nil, false
	}
	known := len(recs)
	ix := &journalIndex{recs: recs}
	var from int64
	if n := len(recs); n > 0 {
		from = recs[n-1].off
	}
	var lastStart int64 = -1
	_, err = s.scanFramesAt(from, func(fr Frame, start int64) {
		if n := len(ix.recs); n == 0 || fr.Seq-ix.recs[n-1].seq >= IndexEvery {
			ix.recs = append(ix.recs, idxRec{fr.Seq, start})
		}
		if fr.Seq > ix.lastSeq {
			ix.lastSeq, ix.lastT, lastStart = fr.Seq, fr.T, start
		}
	})
	if err != nil {
		return nil, err
	}
	if n := len(ix.recs); lastStart >= 0 && ix.recs[n-1].seq < ix.lastSeq {
		ix.recs = append(ix.recs, idxRec{ix.lastSeq, lastStart})
	}
	if !s.zst && Alive(filepath.Dir(jpath)) {
		// The host appends to the index itself. Rewriting it here would
		// replace the file under the host's open descriptor.
		return ix, nil
	}
	switch {
	case !ok:
		_ = writeIndex(ipath, ix.recs)
	case len(ix.recs) > known:
		_ = appendIndex(ipath, ix.recs[known:])
	}
	return ix, nil
}

func appendIndex(path string, recs []idxRec) error {
	var b []byte
	for _, r := range recs {
		b = appendIdx(b, r)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	return errors.Join(err, f.Close())
}

// writeIndex replaces the index file atomically.
func writeIndex(path string, recs []idxRec) error {
	var b []byte
	for _, r := range recs {
		b = appendIdx(b, r)
	}
	return writeFileSync(path, b)
}

func writeFileSync(path string, b []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// tailBackward finds the last frame of a plain journal by reading
// backwards from EOF: a 64 KiB block, doubled until a full line with a
// seq fits. It neither reads nor writes the index.
func tailBackward(f io.ReaderAt, size int64) (int64, int64) {
	for n := int64(64 << 10); ; n *= 2 {
		start := max(size-n, 0)
		buf := make([]byte, size-start)
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return 0, 0
		}
		// Drop a half-written last line and, unless at the start of the
		// file, the first line, which may be cut.
		end := bytes.LastIndexByte(buf, '\n')
		if end < 0 {
			if start == 0 {
				return 0, 0
			}
			continue
		}
		buf = buf[:end]
		lo := 0
		if start > 0 {
			lo = bytes.IndexByte(buf, '\n') + 1
		}
		for len(buf) > lo {
			i := bytes.LastIndexByte(buf, '\n') + 1
			i = max(i, lo)
			var e struct {
				Seq int64 `json:"seq"`
				T   int64 `json:"t"`
			}
			if json.Unmarshal(buf[i:], &e) == nil && e.Seq > 0 {
				return e.Seq, e.T
			}
			buf = buf[:max(i-1, 0)]
		}
		if start == 0 {
			return 0, 0
		}
	}
}

// tailSeekable finds the last frame of a .zst by decompressing its blocks
// from the last one back. Blocks hold whole lines, so each is read alone.
func (s *journalSource) tailSeekable() (int64, int64) {
	for i := len(s.seek) - 1; i >= 0; i-- {
		z := s.seek[i]
		cend := s.data
		if i+1 < len(s.seek) {
			cend = s.seek[i+1].coff
		}
		d, err := zstd.NewReader(io.NewSectionReader(s.f, z.coff, cend-z.coff), zstd.WithDecoderConcurrency(1))
		if err != nil {
			return 0, 0
		}
		b, err := io.ReadAll(d)
		d.Close()
		if err != nil {
			return 0, 0
		}
		if seq, ms := tailBackward(bytes.NewReader(b), int64(len(b))); seq > 0 {
			return seq, ms
		}
	}
	return 0, 0
}

func journalTail(jpath string) (int64, time.Time) {
	s, err := openSource(jpath)
	if err != nil {
		return 0, time.Time{}
	}
	defer func() { _ = s.Close() }()
	var seq, ms int64
	switch {
	case !s.zst:
		seq, ms = tailBackward(s.f, s.size)
	case len(s.seek) > 0:
		seq, ms = s.tailSeekable()
	}
	if seq == 0 && s.zst {
		// No seek table (or no frame in it): the index, rebuilt by one
		// scan if missing.
		if ix, err := ensureIndex(s, jpath); err == nil {
			seq, ms = ix.lastSeq, ix.lastT
		}
	}
	if seq == 0 {
		return 0, time.Time{}
	}
	return seq, time.UnixMilli(ms)
}

// CompressJournal compresses the finished journal in session dir to
// journal.ndjson.zst and removes journal.ndjson. It fails if a host holds
// the session. level is a zstd level from 1 to 22, mapped to the encoder's
// levels: 1-2 fastest, 3-5 default, 6-9 better, 10-22 best. 0 means 3.
//
// The plain file is removed only after the .zst and the index are synced
// and renamed into place. Readers prefer the plain file while both exist,
// and a later call finishes the job.
func CompressJournal(dir string, level int) error {
	if level == 0 {
		level = 3
	}
	if level < 1 || level > 22 {
		return fmt.Errorf("acphost: zstd level %d not in 1..22", level)
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return err
	}
	defer unlock()

	jpath := filepath.Join(dir, JournalFile)
	zpath := filepath.Join(dir, CompressedJournalFile)
	tmp := zpath + ".tmp"
	_ = os.Remove(tmp) // left by a crash mid-compress
	if _, err := os.Stat(jpath); errors.Is(err, os.ErrNotExist) {
		if _, zerr := os.Stat(zpath); zerr == nil {
			return nil // already done
		}
		return err
	}
	if _, err := os.Stat(zpath); err == nil {
		// Renamed into place before a crash: the .zst is complete.
		return os.Remove(jpath)
	}

	in, err := os.Open(jpath)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	recs, err := writeCompressed(in, out, zstd.EncoderLevelFromZstd(level))
	if err == nil {
		err = out.Sync()
	}
	if err = errors.Join(err, out.Close()); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Offsets are into the uncompressed stream, so the index is valid for
	// the plain file too and can go first.
	if err := writeIndex(idxPath(jpath), recs); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, zpath); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return os.Remove(jpath)
}

// writeCompressed writes in to out as one zstd frame per block plus a seek
// table, and returns the index records, one per block start and one for
// the last frame. A trailing half-written line is dropped.
func writeCompressed(in io.Reader, out io.Writer, lvl zstd.EncoderLevel) ([]idxRec, error) {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(lvl), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	defer func() { _ = enc.Close() }()
	var (
		recs       []idxRec
		table      []byte
		block, dst []byte
		nframes    uint32
		blockSeq   int64 // frames in the block
		lastSeq    int64
		lastStart  int64 = -1
		werr       error
	)
	flush := func() {
		if len(block) == 0 || werr != nil {
			return
		}
		dst = enc.EncodeAll(block, dst[:0])
		_, werr = out.Write(dst)
		table = binary.LittleEndian.AppendUint32(table, uint32(len(dst)))
		table = binary.LittleEndian.AppendUint32(table, uint32(len(block)))
		nframes++
		block, blockSeq = block[:0], 0
	}
	_, err = scanLines(in, 0, func(line []byte, start int64) {
		var e struct {
			Seq int64 `json:"seq"`
		}
		ok := json.Unmarshal(line, &e) == nil && e.Seq > 0
		if ok && (blockSeq >= IndexEvery || len(block)+len(line) > maxBlock) {
			flush()
		}
		if ok && blockSeq == 0 {
			if n := len(recs); n == 0 || e.Seq > recs[n-1].seq {
				recs = append(recs, idxRec{e.Seq, start})
			}
		}
		if ok {
			blockSeq++
			if e.Seq > lastSeq {
				lastSeq, lastStart = e.Seq, start
			}
		}
		block = append(block, line...)
	})
	flush()
	if err = errors.Join(err, werr); err != nil {
		return nil, err
	}
	if n := len(recs); lastStart >= 0 && recs[n-1].seq < lastSeq {
		recs = append(recs, idxRec{lastSeq, lastStart})
	}
	var foot []byte
	foot = binary.LittleEndian.AppendUint32(foot, skippableMagic)
	foot = binary.LittleEndian.AppendUint32(foot, uint32(len(table)+9))
	foot = append(foot, table...)
	foot = binary.LittleEndian.AppendUint32(foot, nframes)
	foot = append(foot, 0)
	foot = binary.LittleEndian.AppendUint32(foot, seekableMagic)
	_, err = out.Write(foot)
	return recs, err
}
