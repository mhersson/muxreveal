package ui

import (
	"image/color"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/git/invisible"
	"github.com/mhersson/aim/internal/md"
	"github.com/mhersson/aim/internal/ui/diffview"
	"github.com/mhersson/aim/internal/ui/theme"
)

// mdPainter turns layout lines into theme-coloured cells on one ground.
type mdPainter struct {
	ui     theme.UI
	syntax [diffview.NumClasses]color.Color
	ground theme.Hex
	query  string
}

func newMDPainter(t theme.Theme, ground theme.Hex) mdPainter {
	return mdPainter{ui: t.UI, syntax: reviewPalette(t).Syntax, ground: ground}
}

// withGround is p painting on another ground, such as a cursor row's.
func (p mdPainter) withGround(g theme.Hex) mdPainter {
	p.ground = g

	return p
}

// withQuery is p painting each occurrence of q, ignoring case, in Match.
func (p mdPainter) withQuery(q string) mdPainter {
	p.query = q

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
	case roleConvFrame:
		fg = u.Border
	case roleConvAuthor:
		bold = true
	case roleConvVerb:
		fg = u.Muted
	case roleConvApproved:
		fg = u.Success
	case roleConvChanges:
		fg = u.Warning
	case roleConvMeta:
		fg = u.Subtle
	case roleConvPath:
		fg, bold = u.Dim, true
	case roleHunkAddGutter, roleHunkDelGutter, roleHunkCtxGutter:
		fg, bg = u.Subtle, p.hunkGround(r.Role)
	case roleHunkAdd, roleHunkDel, roleHunkCtx:
		bg = p.hunkGround(r.Role)

		if r.Class > 0 && int(r.Class) < len(p.syntax) && p.syntax[r.Class] != nil {
			return lipgloss.NewStyle().Foreground(p.syntax[r.Class]).Background(bg).
				Bold(bold).Italic(r.Italic).Underline(underline).Strikethrough(r.Strike)
		}
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

// hunkGround is the ground of a hunk line's role: the painter's ground, with
// Success or Error mixed in for an added or removed line.
func (p mdPainter) hunkGround(role md.Role) theme.Hex {
	switch role {
	case roleHunkAdd, roleHunkAddGutter:
		return mixHex(p.ground, p.ui.Success, tintHunkLine)
	case roleHunkDel, roleHunkDelGutter:
		return mixHex(p.ground, p.ui.Error, tintHunkLine)
	}

	return p.ground
}

// line paints l in exactly w cells: cut with … when too wide, then padded on
// the ground. A link goes out as OSC 8 only when linkSafe, and every style and
// link is closed before the line ends.
func (p mdPainter) line(l md.Line, links []md.Link, w int) string {
	if w <= 0 {
		return ""
	}

	l, hit := splitMatches(l, p.query)
	l = cutLine(l, w)

	var sb strings.Builder

	used := 0

	for i, r := range l {
		used += ansi.StringWidth(r.Text)

		st := p.style(r)
		if i < len(hit) && hit[i] {
			st = st.Foreground(p.ui.Match).Bold(true)
		}

		text := st.Render(r.Text)

		if r.Role == md.RolePicture {
			text = lipgloss.NewStyle().Background(p.ground).Render(r.Text)
		} else if r.Link >= 0 && r.Link < len(links) && linkSafe(links[r.Link].URL) {
			text = ansi.SetHyperlink(links[r.Link].URL) + text + ansi.ResetHyperlink()
		}

		sb.WriteString(text)
	}

	if pad := w - used; pad > 0 {
		sb.WriteString(lipgloss.NewStyle().Background(p.ground).Render(strings.Repeat(" ", pad)))
	}

	return sb.String()
}

// splitMatches cuts l's runs at the edges of the occurrences of q in its text
// and says which of the new runs are occurrences. It returns l when there are none.
func splitMatches(l md.Line, q string) (md.Line, []bool) {
	if q == "" || slices.ContainsFunc(l, func(r md.Run) bool { return r.Role == md.RolePicture }) {
		return l, nil
	}

	var sb strings.Builder
	for _, r := range l {
		sb.WriteString(r.Text)
	}

	ms := matchRanges(sb.String(), q)
	if len(ms) == 0 {
		return l, nil
	}

	var (
		out    md.Line
		hit    []bool
		pos, k int
	)

	for _, r := range l {
		end := pos + len(r.Text)

		for at := pos; at < end; {
			for k < len(ms) && ms[k].End <= at {
				k++
			}

			stop, in := end, false

			switch {
			case k < len(ms) && ms[k].Start <= at:
				stop, in = min(ms[k].End, end), true
			case k < len(ms):
				stop = min(ms[k].Start, end)
			}

			seg := r
			seg.Text = r.Text[at-pos : stop-pos]
			out, hit = append(out, seg), append(hit, in)
			at = stop
		}

		if r.Text == "" {
			out, hit = append(out, r), append(hit, false)
		}

		pos = end
	}

	return out, hit
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
