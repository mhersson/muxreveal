package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
)

const (
	// transcriptWindow is how much of a transcript's end CustomName reads. Claude
	// writes the name again all through a session, so the end holds the latest.
	transcriptWindow = 256 << 10
	// maxNameRunes bounds the name: it comes from a file a tool can write.
	maxNameRunes = 120
)

// customTitleMark prefilters lines, so only those that may be a name entry are parsed.
var customTitleMark = []byte(`"custom-title"`)

// CustomName returns the name set with `--name` or `/rename`: the customTitle
// of the last custom-title entry in the last 256 KiB of the transcript. A
// missing file, no entry and an entry with an empty name all mean no name. The
// name is untrusted text, cut to maxNameRunes but not otherwise cleaned. The
// transcript format is Claude Code's own and may change; a change finds no name.
func (Claude) CustomName(ctx context.Context, transcript string) (string, error) {
	if transcript == "" {
		return "", nil
	}

	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("harness: read transcript: %w", err)
	}

	data, err := readTail(transcript, transcriptWindow)
	if err != nil {
		return "", err
	}

	for end := len(data); end > 0; {
		start := bytes.LastIndexByte(data[:end], '\n') + 1
		line := data[start:end]
		end = start - 1

		if !bytes.Contains(line, customTitleMark) {
			continue
		}

		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("harness: read transcript: %w", err)
		}

		var e struct {
			Type        string `json:"type"`
			CustomTitle string `json:"customTitle"`
		}

		if json.Unmarshal(line, &e) != nil || e.Type != "custom-title" {
			continue
		}

		return capRunes(e.CustomTitle, maxNameRunes), nil
	}

	return "", nil
}

// readTail returns the last n bytes of the file at path, from the start of the
// first whole line in them; the whole file when it is smaller. A missing file
// gives nothing and no error.
func readTail(path string, n int64) ([]byte, error) {
	// O_NONBLOCK keeps a named pipe at the path from blocking the open.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("harness: open transcript: %w", err)
	}

	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("harness: stat transcript: %w", err)
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("harness: transcript %s is not a regular file", path)
	}

	// One byte before the window tells whether its first line starts there.
	start := max(info.Size()-n, 0)
	from := max(start-1, 0)
	buf := make([]byte, info.Size()-from)

	read, err := f.ReadAt(buf, from)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("harness: read transcript: %w", err)
	}

	buf = buf[:read]

	if start == 0 {
		return buf, nil
	}

	// The first line is cut by the window unless the byte before it is a newline.
	if i := bytes.IndexByte(buf, '\n'); i >= 0 {
		return buf[i+1:], nil
	}

	return nil, nil
}

// capRunes cuts s to at most n runes.
func capRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}

	i := 0

	for pos := range s {
		if i == n {
			return s[:pos]
		}

		i++
	}

	return s
}
