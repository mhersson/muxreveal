package ui

import (
	"image/color"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/git/invisible"
	"github.com/mhersson/muxreveal/internal/md"
	"github.com/mhersson/muxreveal/internal/ui/diffview"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// mdPainter turns layout lines into theme-coloured cells on one ground.
type mdPainter struct {
	ui     theme.UI
	syntax [diffview.NumClasses]color.Color
	ground theme.Hex
}

func newMDPainter(t theme.Theme, ground theme.Hex) mdPainter {
	return mdPainter{ui: t.UI, syntax: reviewPalette(t).Syntax, ground: ground}
}

// withGround is p painting on another ground, such as a cursor row's.
func (p mdPainter) withGround(g theme.Hex) mdPainter {
	p.ground = g

	return p
}

// style is the look of a run: its role's colours, then the run's own attributes.
func (p mdPainter) style(r md.Run) lipgloss.Style {
	u := p.ui
	fg, bg, bold, underline := u.Fg, p.ground, r.Bold, r.Underline

	switch r.Role {
	case md.RoleHeading1, md.RoleHeading2:
		fg, bold = u.Accent, true
	case md.RoleHeading3, md.RoleSummary, md.RoleTableHeader:
		bold = true
	case md.RoleRule, md.RoleCodeFrame, md.RoleQuoteBar, md.RoleTableFrame, md.RoleFootnoteRule:
		fg = u.Border
	case md.RoleCode:
		fg, bg = u.Code, u.Surface
	case md.RoleLink:
		fg, underline = u.Info, true
	case md.RoleKey:
		fg, bold = u.Key, true
	case md.RoleBullet:
		fg = u.Accent
	case md.RoleTaskDone:
		fg, bold = u.Success, true
	case md.RoleTaskDoneText, md.RoleQuoteText, md.RoleFootnoteText:
		fg = u.Dim
	case md.RoleTaskOpen, md.RoleLineNumber, md.RoleImage:
		fg = u.Muted
	case md.RoleCodeLang, md.RoleFoldHint, md.RoleImageHost:
		fg = u.Subtle
	case md.RoleHidden:
		fg, bold = u.Error, true
	case md.RoleAlertNote, md.RoleFootnoteRef, md.RoleFootnoteNumber:
		fg = u.Info
	case md.RoleAlertTip:
		fg = u.Success
	case md.RoleAlertImportant:
		fg = u.Accent
	case md.RoleAlertWarning:
		fg = u.Warning
	case md.RoleAlertCaution:
		fg = u.Error
	case md.RoleSummaryMark:
		fg, bold = u.Accent, true
	case md.RoleCodeText:
		if int(r.Class) < len(p.syntax) && p.syntax[r.Class] != nil {
			return lipgloss.NewStyle().Foreground(p.syntax[r.Class]).Background(bg).
				Bold(bold).Italic(r.Italic).Underline(underline).Strikethrough(r.Strike)
		}
	}

	if r.Path != "" {
		fg, underline = u.Info, true
	}

	return lipgloss.NewStyle().Foreground(fg).Background(bg).
		Bold(bold).Italic(r.Italic).Underline(underline).Strikethrough(r.Strike)
}

// line paints l in exactly w cells: cut with … when too wide, then padded on
// the ground. A link goes out as OSC 8 only when linkSafe, and every style and
// link is closed before the line ends.
func (p mdPainter) line(l md.Line, links []md.Link, w int) string {
	if w <= 0 {
		return ""
	}

	l = cutLine(l, w)

	var sb strings.Builder

	used := 0

	for _, r := range l {
		used += ansi.StringWidth(r.Text)

		text := p.style(r).Render(r.Text)

		if r.Link >= 0 && r.Link < len(links) && linkSafe(links[r.Link].URL) {
			text = ansi.SetHyperlink(links[r.Link].URL) + text + ansi.ResetHyperlink()
		}

		sb.WriteString(text)
	}

	if pad := w - used; pad > 0 {
		sb.WriteString(lipgloss.NewStyle().Background(p.ground).Render(strings.Repeat(" ", pad)))
	}

	return sb.String()
}

// cutLine is l cut to w cells, ending in … in the last run's style when
// anything is left out. A wide character is never split.
func cutLine(l md.Line, w int) md.Line {
	total := 0
	for _, r := range l {
		total += ansi.StringWidth(r.Text)
	}

	if total <= w {
		return l
	}

	var out md.Line

	budget := w - 1

	for _, r := range l {
		rw := ansi.StringWidth(r.Text)
		if rw <= budget {
			out = append(out, r)
			budget -= rw

			continue
		}

		cut := r
		cut.Text = ansi.Truncate(r.Text, budget, "") + "…"
		out = append(out, cut)

		return out
	}

	return out
}

// linkSafe reports whether url may go out as OSC 8: http, https or mailto with
// no control or invisible character, since a terminal reads those inside the
// sequence.
func linkSafe(url string) bool {
	if !utf8.ValidString(url) {
		return false
	}

	lower := strings.ToLower(url)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "mailto:") {
		return false
	}

	for _, r := range url {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) || invisible.Rune(r) {
			return false
		}
	}

	return true
}
