package github

import (
	"bytes"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mhersson/aim/internal/git/invisible"
)

// LogKind says how a job log line is drawn.
type LogKind uint8

// The kinds of a job log line.
const (
	LogText LogKind = iota
	// LogGroupHead is a group's head line.
	LogGroupHead
	LogError
	LogWarning
	LogNotice
	LogCommand
	LogDebug
)

// LogSpan is a run of text in one style. Color is -1 for the default or 0-15
// for an ANSI colour.
type LogSpan struct {
	Text  string
	Color int8
	Bold  bool
}

// LogLine is one line of a job log. At is zero without a timestamp; Group is
// -1 outside a group, else the group's index (its head line too).
type LogLine struct {
	At    time.Time
	Kind  LogKind
	Spans []LogSpan
	Group int
}

// LogGroup is a "##[group]" section. Head is its head line and End one past
// its last line.
type LogGroup struct {
	Title string
	Head  int
	End   int
}

// JobLog is a parsed job log. FirstError is the index of the first error
// line, -1 for none; Cut says the log starts partway.
type JobLog struct {
	Lines      []LogLine
	Groups     []LogGroup
	FirstError int
	Cut        bool
}

var bom = []byte("\xef\xbb\xbf")

// ParseJobLog turns the bytes of a job log into lines. Only printable text
// survives in the spans: every escape sequence but a foreground colour or
// bold is dropped, as are controls and invisible characters. When cut, the
// first line is partial and is dropped.
func ParseJobLog(data []byte, cut bool) JobLog {
	log := JobLog{FirstError: -1, Cut: cut}
	data = bytes.TrimPrefix(data, bom)
	open := -1
	closeGroup := func() {
		if open >= 0 {
			log.Groups[open].End = len(log.Lines)
			open = -1
		}
	}
	first := true

	for len(data) > 0 {
		var raw []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			raw, data = data[:i], data[i+1:]
		} else {
			raw, data = data, nil
		}

		if first && cut {
			first = false

			continue
		}

		first = false
		raw = bytes.TrimRight(raw, "\r")

		var line LogLine

		line.At, raw = logTime(raw)

		kind, prefix, name, raw := logMarker(raw)
		if name == "endgroup" {
			closeGroup()

			continue
		}

		line.Kind = kind

		if i := bytes.LastIndexByte(raw, '\r'); i >= 0 {
			raw = raw[i+1:]
		}

		line.Spans = logSpans(prefix, raw)

		if kind == LogGroupHead {
			closeGroup()

			var title strings.Builder
			for _, s := range line.Spans {
				title.WriteString(s.Text)
			}

			log.Groups = append(log.Groups, LogGroup{Title: title.String(), Head: len(log.Lines)})
			open = len(log.Groups) - 1
		}

		line.Group = open

		if kind == LogError && log.FirstError < 0 {
			log.FirstError = len(log.Lines)
		}

		log.Lines = append(log.Lines, line)
	}

	closeGroup()

	return log
}

// logTime splits a leading RFC 3339 UTC time and its space off b.
func logTime(b []byte) (time.Time, []byte) {
	sp := bytes.IndexByte(b, ' ')
	if sp < 20 || sp > 40 || b[sp-1] != 'Z' {
		return time.Time{}, b
	}

	t, err := time.Parse(time.RFC3339Nano, string(b[:sp]))
	if err != nil {
		return time.Time{}, b
	}

	return t.UTC(), b[sp+1:]
}

// logMarker reads a "##[name]" marker at the start of b. It returns the
// line's kind, the text to put before the rest, the marker's name and the
// rest. A line without a marker is LogText.
func logMarker(b []byte) (LogKind, string, string, []byte) {
	if !bytes.HasPrefix(b, []byte("##[")) {
		return LogText, "", "", b
	}

	end := bytes.IndexByte(b, ']')
	if end < 0 || end > 3+32 {
		return LogText, "", "", b
	}

	name := string(b[3:end])
	rest := b[end+1:]

	switch name {
	case "group":
		return LogGroupHead, "", name, rest
	case "endgroup":
		return LogText, "", name, rest
	case "error":
		return LogError, "Error: ", name, rest
	case "warning":
		return LogWarning, "Warning: ", name, rest
	case "notice":
		return LogNotice, "Notice: ", name, rest
	case "command":
		return LogCommand, "", name, rest
	case "debug":
		return LogDebug, "", name, rest
	}

	return LogText, "", name, rest
}

// logSpans scans b into styled spans, dropping every escape sequence but
// SGR and every unprintable character.
func logSpans(prefix string, b []byte) []LogSpan {
	var (
		spans []LogSpan
		cur   strings.Builder
		color = int8(-1)
		bold  bool
	)

	flush := func() {
		if cur.Len() == 0 {
			return
		}

		spans = append(spans, LogSpan{Text: cur.String(), Color: color, Bold: bold})
		cur.Reset()
	}

	cur.WriteString(prefix)

	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0x1b:
			end, params, sgr := logEscape(b, i)
			if sgr {
				nc, nb := applySGR(params, color, bold)
				if nc != color || nb != bold {
					flush()

					color, bold = nc, nb
				}
			}

			i = end
		case c < utf8.RuneSelf:
			if c == '\t' || (c >= 0x20 && c != 0x7f) {
				cur.WriteByte(c)
			}

			i++
		default:
			r, n := utf8.DecodeRune(b[i:])

			i += n
			if r == utf8.RuneError && n == 1 {
				continue
			}

			if (r >= 0x80 && r <= 0x9f) || invisible.Rune(r) {
				continue
			}

			cur.WriteRune(r)
		}
	}

	flush()

	return spans
}

// logEscape reads the escape sequence at b[i] and returns the index after
// it, plus the parameters when it is an SGR sequence.
func logEscape(b []byte, i int) (int, []byte, bool) {
	i++
	if i >= len(b) {
		return i, nil, false
	}

	switch b[i] {
	case '[':
		start := i + 1
		for j := start; j < len(b); j++ {
			if b[j] >= 0x40 && b[j] <= 0x7e {
				return j + 1, b[start:j], b[j] == 'm'
			}

			if b[j] < 0x20 || b[j] > 0x3f {
				// Not a valid CSI: stop before the stray byte.
				return j, nil, false
			}
		}

		return len(b), nil, false
	case ']', 'P', 'X', '^', '_':
		for j := i + 1; j < len(b); j++ {
			if b[j] == 0x07 {
				return j + 1, nil, false
			}

			if b[j] == 0x1b && j+1 < len(b) && b[j+1] == '\\' {
				return j + 2, nil, false
			}
		}

		return len(b), nil, false
	}

	for i < len(b) && b[i] >= 0x20 && b[i] <= 0x2f {
		i++
	}

	if i < len(b) && b[i] >= 0x30 && b[i] <= 0x7e {
		i++
	}

	return i, nil, false
}

// applySGR applies the parameters of one SGR sequence to the colour and bold.
func applySGR(params []byte, color int8, bold bool) (int8, bool) {
	var nums []int
	for p := range bytes.SplitSeq(params, []byte{';'}) {
		nums = append(nums, sgrNumber(p))
	}

	for k := 0; k < len(nums); k++ {
		n := nums[k]
		switch {
		case n == 0:
			color, bold = -1, false
		case n == 1:
			bold = true
		case n == 22:
			bold = false
		case n >= 30 && n <= 37:
			color = int8(n - 30)
		case n >= 90 && n <= 97:
			color = int8(n - 90 + 8)
		case n == 39:
			color = -1
		case n == 38 || n == 48:
			fg := n == 38

			kind := -1
			if k+1 < len(nums) {
				kind = nums[k+1]
			}

			switch kind {
			case 5:
				if fg {
					color = -1
					if k+2 < len(nums) && nums[k+2] >= 0 && nums[k+2] < 16 {
						color = int8(nums[k+2])
					}
				}

				k += 2
			case 2:
				if fg {
					color = -1
				}

				k += 4
			default:
				k = len(nums)
			}
		}
	}

	return color, bold
}

// sgrNumber reads a decimal parameter; empty is 0 and anything else that is
// not a plain number is -1, which no case matches.
func sgrNumber(p []byte) int {
	n := 0

	for _, c := range p {
		if c < '0' || c > '9' {
			return -1
		}

		if n = n*10 + int(c-'0'); n > 1000 {
			return -1
		}
	}

	return n
}
