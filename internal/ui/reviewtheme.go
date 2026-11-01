package ui

import (
	"github.com/mhersson/aim/internal/ui/diffview"
	"github.com/mhersson/aim/internal/ui/theme"
)

// reviewPalette is the diff view's palette in the theme's colours.
func reviewPalette(t theme.Theme) diffview.Palette {
	p, u := t.Palette, t.UI

	pal := diffview.Palette{
		Depth:  diffview.DepthTrue,
		Ground: u.Bg,
		Fg:     u.Fg,
		Accent: u.Accent,
		Add:    u.Success,
		Delete: u.Error,
	}
	pal.Syntax[diffview.Plain] = p.Text
	pal.Syntax[diffview.Keyword] = p.Mauve
	pal.Syntax[diffview.Type] = p.Yellow
	pal.Syntax[diffview.Func] = p.Blue
	pal.Syntax[diffview.Builtin] = p.Sky
	pal.Syntax[diffview.String] = p.Green
	pal.Syntax[diffview.Number] = p.Peach
	pal.Syntax[diffview.Comment] = p.Overlay0
	pal.Syntax[diffview.Operator] = p.Sky
	pal.Syntax[diffview.Punct] = p.Overlay2
	pal.Syntax[diffview.Preproc] = p.Pink
	pal.Syntax[diffview.Tag] = p.Mauve
	pal.Syntax[diffview.Attr] = p.Blue
	pal.Syntax[diffview.Heading] = p.Peach
	pal.Syntax[diffview.Meta] = p.Overlay1

	return pal
}
