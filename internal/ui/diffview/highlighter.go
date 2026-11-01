//go:build !js

package diffview

import (
	"sync"

	"github.com/alecthomas/chroma/v2"

	"github.com/mhersson/aim/internal/ui/diffview/lexers"
)

// noLexerT marks a file name no lexer matches in a Highlighter's cache.
type noLexerT struct{}

// Highlighter tokenises code and caches the lexers it finds. The zero value
// is ready to use; use it through a pointer. It is safe for concurrent use.
type Highlighter struct {
	once sync.Once
	reg  *chroma.LexerRegistry

	// lexers holds the lexer for each file name (noLexerT for none).
	// Matching a name runs every lexer's patterns, and the answer for a name
	// never changes.
	lexers sync.Map // string -> chroma.Lexer or noLexerT
	// langs holds the lexer for each language name a lexer answers to.
	langs sync.Map // string -> chroma.Lexer

	// misses holds language names no lexer answers to, at most langMissMax:
	// the names come from agents' text, so the set is emptied when it fills.
	missMu sync.Mutex
	misses map[string]bool
}

// registry returns the lexer registry, built on first use.
func (h *Highlighter) registry() *chroma.LexerRegistry {
	h.once.Do(func() { h.reg = lexers.NewRegistry() })

	return h.reg
}
