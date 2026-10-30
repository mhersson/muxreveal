package git

import (
	"crypto/sha256"
	"encoding/hex"
)

// HunkID identifies a hunk by its path and its changed lines, so it survives
// moved line numbers, other context and a renamed old path.
func HunkID(path string, h Hunk) string {
	sum := sha256.New()
	sum.Write([]byte(path))
	sum.Write([]byte{0})

	for _, l := range h.Lines {
		if l.Kind == Context {
			continue
		}

		sum.Write([]byte{byte(l.Kind)})
		sum.Write([]byte(l.Text))

		if l.NoNewline {
			sum.Write([]byte{'\\'})
		}

		sum.Write([]byte{'\n'})
	}

	return hex.EncodeToString(sum.Sum(nil)[:16])
}
