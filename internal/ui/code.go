package ui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"github.com/mhersson/muxreveal/internal/ui/theme"
)

const tabWidth = 4

// highlight colours src as lang with the theme's syntax style, on the panel
// background. An unknown lang renders as plain text. Tabs become spaces, since
// terminals move a raw tab to a tab stop and width measuring counts it as zero.
func highlight(src, lang string, t theme.Theme) ([]string, error) {
	style, err := t.Syntax()
	if err != nil {
		return nil, err
	}

	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Fallback
	}

	it, err := chroma.Coalesce(lexer).Tokenise(nil, src)
	if err != nil {
		return nil, fmt.Errorf("tokenise %s: %w", lang, err)
	}

	var (
		lines []string
		line  strings.Builder
	)

	for tok := it(); tok != chroma.EOF; tok = it() {
		st := tokenStyle(style.Get(tok.Type), t)

		parts := strings.Split(strings.ReplaceAll(tok.Value, "\t", strings.Repeat(" ", tabWidth)), "\n")
		for i, part := range parts {
			if i > 0 {
				lines = append(lines, line.String())
				line.Reset()
			}

			if part != "" {
				line.WriteString(st.Render(part))
			}
		}
	}

	if line.Len() > 0 {
		lines = append(lines, line.String())
	}

	return lines, nil
}

func tokenStyle(e chroma.StyleEntry, t theme.Theme) lipgloss.Style {
	var fg color.Color = t.UI.Fg
	if e.Colour.IsSet() {
		fg = lipgloss.Color(e.Colour.String())
	}

	return lipgloss.NewStyle().
		Background(t.UI.Panel).
		Foreground(fg).
		Bold(e.Bold == chroma.Yes).
		Italic(e.Italic == chroma.Yes).
		Underline(e.Underline == chroma.Yes)
}
