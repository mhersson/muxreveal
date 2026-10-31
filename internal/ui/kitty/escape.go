// Package kitty builds Kitty graphics protocol escapes and Unicode
// placeholder cells. It has no terminal-UI dependencies.
package kitty

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

const (
	// QueryID is the image id of the support probe.
	QueryID = 31
	// MaxID is the largest id a placeholder foreground colour can carry.
	MaxID = 1<<24 - 1
	// CellSizeQuery asks the terminal for its cell size in pixels.
	CellSizeQuery = "\x1b[16t"
)

// Query probes for Kitty graphics support; the trailing device-attributes
// request makes a terminal without support still answer.
func Query() string {
	return ansi.KittyGraphics([]byte("AAAA"), "i="+strconv.Itoa(QueryID), "s=1", "v=1", "a=q", "t=d", "f=24") +
		CellSizeQuery + "\x1b[c"
}

// Transmit uploads png as image id in chunks and creates a virtual placement
// of cols x rows cells for Unicode placeholders.
func Transmit(id uint32, png []byte, cols, rows int) string {
	enc := base64.StdEncoding.EncodeToString(png)
	img := "i=" + strconv.FormatUint(uint64(id), 10)

	var b strings.Builder

	for first := true; ; first = false {
		chunk := enc
		more := 0

		if len(chunk) > kitty.MaxChunkSize {
			chunk, enc = chunk[:kitty.MaxChunkSize], chunk[kitty.MaxChunkSize:]
			more = 1
		}

		m := "m=" + strconv.Itoa(more)
		if first {
			b.WriteString(ansi.KittyGraphics([]byte(chunk), "a=t", "f=100", "t=d", img, "q=2", m))
		} else {
			b.WriteString(ansi.KittyGraphics([]byte(chunk), m))
		}

		if more == 0 {
			break
		}
	}

	b.WriteString(ansi.KittyGraphics(nil, "a=p", "U=1", img,
		"c="+strconv.Itoa(cols), "r="+strconv.Itoa(rows), "q=2"))

	return b.String()
}

// Delete frees image id and its data.
func Delete(id uint32) string {
	return ansi.KittyGraphics(nil, "a=d", "d=I", "i="+strconv.FormatUint(uint64(id), 10), "q=2")
}

// DeleteAll frees every image and its data.
func DeleteAll() string {
	return ansi.KittyGraphics(nil, "a=d", "d=A", "q=2")
}

// Placeholders returns cols placeholder cells for image row row.
func Placeholders(row, cols int) string {
	var b strings.Builder
	for c := range cols {
		b.WriteRune(kitty.Placeholder)
		b.WriteRune(kitty.Diacritic(row))
		b.WriteRune(kitty.Diacritic(c))
	}

	return b.String()
}

// IDColor returns the placeholder foreground that encodes id.
func IDColor(id uint32) string {
	return fmt.Sprintf("#%06x", id&MaxID)
}
