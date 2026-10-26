// Package rec is the timestamped PTY recording format: pane output and
// resizes, used to replay real sessions through the emulator.
package rec

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// MaxChunk bounds one data record so a corrupt length cannot force a huge allocation.
const MaxChunk = 1 << 20

const (
	header      = "MUXREC 1\n"
	spikeMagic  = "VTSPIKE-REC"
	typeData    = 'D'
	typeResize  = 'R'
	fixedLen    = 12 // 8 bytes of nanoseconds plus a uint32 length or two uint16 sizes
	maxHdrBytes = 64
)

// ErrTruncated marks a recording cut off inside a record; the complete records before it are still returned.
var ErrTruncated = errors.New("recording truncated")

// Record is one data chunk or one resize. Data is nil for a resize.
type Record struct {
	At         time.Duration // since the recording started
	Data       []byte
	Cols, Rows int
}

// Writer appends records with one underlying Write each and no buffering, so
// a crash loses at most the record being written.
type Writer struct {
	mu    sync.Mutex
	w     io.Writer
	now   func() time.Time
	start time.Time
	last  time.Duration
}

// NewWriter writes the header and the initial size; the clock starts here.
func NewWriter(w io.Writer, cols, rows int, now func() time.Time) (*Writer, error) {
	if _, err := io.WriteString(w, header); err != nil {
		return nil, fmt.Errorf("write header: %w", err)
	}

	rw := &Writer{w: w, now: now, start: now()}
	if err := rw.resize(cols, rows, false); err != nil {
		return nil, err
	}

	return rw, nil
}

// stamp returns the time since start, never going backwards. Callers hold mu.
func (w *Writer) stamp() time.Duration {
	w.last = max(w.now().Sub(w.start), w.last)

	return w.last
}

// Data records output bytes.
func (w *Writer) Data(b []byte) error {
	if len(b) > MaxChunk {
		return fmt.Errorf("chunk of %d bytes exceeds %d", len(b), MaxChunk)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	buf := make([]byte, 1+fixedLen+len(b))
	buf[0] = typeData
	binary.BigEndian.PutUint64(buf[1:], uint64(w.stamp()))
	binary.BigEndian.PutUint32(buf[9:], uint32(len(b)))
	copy(buf[1+fixedLen:], b)

	if _, err := w.w.Write(buf); err != nil {
		return fmt.Errorf("write data record: %w", err)
	}

	return nil
}

// Resize records a new terminal size.
func (w *Writer) Resize(cols, rows int) error { return w.resize(cols, rows, true) }

// resize writes a resize record; the initial one is stamped 0 without reading the clock.
func (w *Writer) resize(cols, rows int, stamped bool) error {
	if cols <= 0 || rows <= 0 || cols > 0xffff || rows > 0xffff {
		return fmt.Errorf("resize to %dx%d out of range", cols, rows)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	var buf [1 + fixedLen]byte

	buf[0] = typeResize
	if stamped {
		binary.BigEndian.PutUint64(buf[1:], uint64(w.stamp()))
	}

	binary.BigEndian.PutUint16(buf[9:], uint16(cols))
	binary.BigEndian.PutUint16(buf[11:], uint16(rows))

	if _, err := w.w.Write(buf[:]); err != nil {
		return fmt.Errorf("write resize record: %w", err)
	}

	return nil
}

// Read parses a recording: the typed format, or the spike's
// "VTSPIKE-REC 1 <cols> <rows>" files. On a cut tail it returns the complete
// records and an error wrapping ErrTruncated; on a corrupt record it returns
// the records before it and an error.
func Read(r io.Reader) ([]Record, error) {
	br := bufio.NewReaderSize(r, maxHdrBytes)

	line, err := br.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	switch {
	case line == header:
		return readTyped(br)
	case strings.HasPrefix(line, spikeMagic+" "):
		return readSpike(br, line)
	}

	return nil, fmt.Errorf("bad header %q", line)
}

func readTyped(br *bufio.Reader) ([]Record, error) {
	var (
		recs []Record
		last time.Duration
	)

	for {
		typ, err := br.ReadByte()
		if errors.Is(err, io.EOF) {
			if len(recs) == 0 {
				return nil, fmt.Errorf("no records: %w", ErrTruncated)
			}

			return recs, nil
		}

		if err != nil {
			return recs, fmt.Errorf("record %d type: %w", len(recs), err)
		}

		var fixed [fixedLen]byte
		if _, err := io.ReadFull(br, fixed[:]); err != nil {
			return recs, fmt.Errorf("record %d header: %w", len(recs), ErrTruncated)
		}

		at := time.Duration(int64(binary.BigEndian.Uint64(fixed[:8])))
		if at < last {
			return recs, fmt.Errorf("record %d at %v is before the previous record at %v", len(recs), at, last)
		}

		last = at

		var rec Record

		switch typ {
		case typeResize:
			rec = Record{At: at, Cols: int(binary.BigEndian.Uint16(fixed[8:])), Rows: int(binary.BigEndian.Uint16(fixed[10:]))}
			if rec.Cols == 0 || rec.Rows == 0 {
				return recs, fmt.Errorf("record %d resizes to %dx%d", len(recs), rec.Cols, rec.Rows)
			}
		case typeData:
			if len(recs) == 0 {
				return nil, errors.New("first record is not a resize")
			}

			n := binary.BigEndian.Uint32(fixed[8:])
			if n > MaxChunk {
				return recs, fmt.Errorf("record %d length %d exceeds %d", len(recs), n, MaxChunk)
			}

			data := make([]byte, n)
			if _, err := io.ReadFull(br, data); err != nil {
				return recs, fmt.Errorf("record %d data: %w", len(recs), ErrTruncated)
			}

			rec = Record{At: at, Data: data}
		default:
			return recs, fmt.Errorf("record %d has unknown type %q", len(recs), typ)
		}

		recs = append(recs, rec)
	}
}

// readSpike reads the spike's untyped chunks as an initial resize plus data records.
func readSpike(br *bufio.Reader, line string) ([]Record, error) {
	var (
		m          string
		ver        int
		cols, rows int
	)

	if _, err := fmt.Sscanf(strings.TrimSuffix(line, "\n"), "%s %d %d %d", &m, &ver, &cols, &rows); err != nil {
		return nil, fmt.Errorf("bad header %q: %w", line, err)
	}

	if ver != 1 || cols <= 0 || rows <= 0 || cols > 0xffff || rows > 0xffff {
		return nil, fmt.Errorf("bad header %q", line)
	}

	recs := []Record{{Cols: cols, Rows: rows}}

	var last time.Duration

	for {
		var hdr [fixedLen]byte

		_, err := io.ReadFull(br, hdr[:])
		if errors.Is(err, io.EOF) {
			return recs, nil
		}

		if err != nil {
			return recs, fmt.Errorf("record %d header: %w", len(recs), ErrTruncated)
		}

		at := time.Duration(int64(binary.BigEndian.Uint64(hdr[:8])))
		n := binary.BigEndian.Uint32(hdr[8:])

		if n > MaxChunk {
			return recs, fmt.Errorf("record %d length %d exceeds %d", len(recs), n, MaxChunk)
		}

		if at < last {
			return recs, fmt.Errorf("record %d at %v is before the previous record at %v", len(recs), at, last)
		}

		data := make([]byte, n)
		if _, err := io.ReadFull(br, data); err != nil {
			return recs, fmt.Errorf("record %d data: %w", len(recs), ErrTruncated)
		}

		last = at
		recs = append(recs, Record{At: at, Data: data})
	}
}

// Load reads the recording at path.
func Load(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open recording: %w", err)
	}

	defer func() { _ = f.Close() }()

	return Read(f)
}

// Duration is the At of the last record.
func Duration(recs []Record) time.Duration {
	if len(recs) == 0 {
		return 0
	}

	return recs[len(recs)-1].At
}
