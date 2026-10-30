package ui

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/mhersson/muxreveal/internal/md"
)

// mdView is a cursor, scrolling, folds, heading jumps, search and link targets
// over one mdDoc. The owner calls size before keys and draws.
type mdView struct {
	doc         *mdDoc
	cursor, top int

	w, h  int
	icons bool
}

// mdTarget is somewhere a rendered line leads: a URL or a local path.
type mdTarget struct{ Text, URL, Path string }

// size sets the view's cells; the text is laid out four cells narrower.
func (v *mdView) size(w, h int, icons bool) {
	v.w, v.h, v.icons = w, h, icons
	v.clamp()
}

func (v *mdView) textWidth() int { return max(v.w-4, 1) }

// layout is the doc laid out for the view's text width.
func (v *mdView) layout() md.Layout {
	if v.doc == nil {
		return md.Layout{}
	}

	return v.doc.layout(v.textWidth(), v.icons)
}

// clamp keeps the cursor on a line and in the window.
func (v *mdView) clamp() {
	n := len(v.layout().Lines)
	v.cursor = max(min(v.cursor, n-1), 0)

	h := max(v.h, 1)
	if v.cursor < v.top {
		v.top = v.cursor
	}

	if v.cursor >= v.top+h {
		v.top = v.cursor - h + 1
	}

	v.top = max(min(v.top, n-h), 0)
}

func (v *mdView) move(delta int) {
	v.cursor += delta
	v.clamp()
}

// page moves half the height.
func (v *mdView) page(dir int) { v.move(dir * max(v.h/2, 1)) }

func (v *mdView) end(bottom bool) {
	v.cursor = 0
	if bottom {
		v.cursor = len(v.layout().Lines) - 1
	}

	v.clamp()
}

func isHeadingLine(l md.Line) bool {
	return slices.ContainsFunc(l, func(r md.Run) bool {
		return r.Role == md.RoleHeading1 || r.Role == md.RoleHeading2 || r.Role == md.RoleHeading3
	})
}

// heading moves to the first line of the next (dir > 0) or previous heading and
// stays put when there is none.
func (v *mdView) heading(dir int) {
	lay := v.layout()
	start := func(i int) bool {
		return isHeadingLine(lay.Lines[i]) && (i == 0 || !isHeadingLine(lay.Lines[i-1]) || lay.Source[i] != lay.Source[i-1])
	}

	for i := v.cursor + dir; i >= 0 && i < len(lay.Lines); i += dir {
		if start(i) {
			v.cursor = i
			v.clamp()

			return
		}
	}
}

// find moves to the next line holding q, ignoring case, from the line after the
// cursor and wrapping.
func (v *mdView) find(q string) bool {
	lay := v.layout()
	if q == "" || len(lay.Lines) == 0 {
		return false
	}

	plain := v.doc.text()
	q = strings.ToLower(q)

	for k := 1; k <= len(plain); k++ {
		i := (v.cursor + k) % len(plain)
		if strings.Contains(strings.ToLower(plain[i]), q) {
			v.cursor = i
			v.clamp()

			return true
		}
	}

	return false
}

// fold toggles the fold whose head is the cursor's line; false when there is none.
func (v *mdView) fold() bool {
	for _, f := range v.layout().Folds {
		if f.Line == v.cursor {
			v.doc.toggleFold(f.ID)
			v.clamp()

			return true
		}
	}

	return false
}

func (v *mdView) targets() []mdTarget { return lineTargets(v.layout(), v.cursor) }

// lineTargets are the links and paths of line i in order, each once, and the
// link of an image on it. mailto links are left out: the terminal opens them.
func lineTargets(lay md.Layout, i int) []mdTarget {
	if i < 0 || i >= len(lay.Lines) {
		return nil
	}

	var out []mdTarget

	seen := map[string]int{}
	last := ""

	add := func(key, text string, t mdTarget) {
		if at, ok := seen[key]; ok {
			if key == last {
				out[at].Text += text
			}

			last = key

			return
		}

		t.Text = text
		seen[key] = len(out)
		out = append(out, t)
		last = key
	}

	for _, r := range lay.Lines[i] {
		switch {
		case r.Link >= 0 && r.Link < len(lay.Links):
			u := lay.Links[r.Link].URL
			if !strings.HasPrefix(strings.ToLower(u), "mailto:") {
				add("u:"+u, r.Text, mdTarget{URL: u})

				continue
			}

			last = ""
		case r.Path != "":
			add("p:"+r.Path, r.Text, mdTarget{Path: r.Path})
		default:
			last = ""
		}
	}

	for _, im := range lay.Images {
		if im.Line != i || im.Link < 0 || im.Link >= len(lay.Links) {
			continue
		}

		u := lay.Links[im.Link].URL
		if strings.HasPrefix(strings.ToLower(u), "mailto:") {
			continue
		}

		if at, ok := seen["u:"+u]; ok {
			if im.Alt != "" {
				out[at].Text = im.Alt
			}

			continue
		}

		seen["u:"+u] = len(out)
		out = append(out, mdTarget{Text: im.Alt, URL: u})
	}

	for k := range out {
		out[k].Text = strings.TrimSpace(out[k].Text)
	}

	return out
}

// sourceLine is the first source line of the cursor's line, 1 when no source
// made it.
func (v *mdView) sourceLine() int {
	src := v.layout().Source
	if v.cursor < 0 || v.cursor >= len(src) || src[v.cursor].First == 0 {
		return 1
	}

	return src[v.cursor].First
}

// goSource puts the cursor on the first line of the narrowest block holding
// source line n, else the next block, else the last line.
func (v *mdView) goSource(n int) {
	v.cursor = sourceLineIndex(v.layout().Source, n)
	v.clamp()
}

// sourceLineIndex is the layout line of the narrowest range holding source
// line n, else of the first range after it, else the last line.
func sourceLineIndex(src []md.SourceRange, n int) int {
	best, width := -1, 0

	for i, r := range src {
		if r.First != 0 && r.First <= n && n <= r.Last && (best < 0 || r.Last-r.First < width) {
			best, width = i, r.Last-r.First
		}
	}

	if best < 0 {
		next := 0

		for i, r := range src {
			if r.First > n && (best < 0 || r.First < next) {
				best, next = i, r.First
			}
		}
	}

	if best < 0 {
		best = len(src) - 1
	}

	return best
}

// draw is h rows of w cells. Cell 0 of the cursor's row holds ▎ on the cursor
// ground, cell 1 is blank, the text starts at cell 2 and two cells stay free
// on the right. An unfocused view shows no cursor.
func (v *mdView) draw(p mdPainter, focused bool) []string {
	lay := v.layout()
	lead, tw := min(2, v.w), max(v.w-4, 0)
	trail := max(v.w-lead-tw, 0)
	rows := make([]string, 0, v.h)

	pad := func(pp mdPainter, n int) string {
		return lipgloss.NewStyle().Background(pp.ground).Render(strings.Repeat(" ", n))
	}

	for r := range v.h {
		i := v.top + r
		pp, mark := p, pad(p, lead)

		if i == v.cursor && focused && i < len(lay.Lines) {
			pp = p.withGround(mixHex(p.ground, p.ui.Accent, tintCursor))

			mark = lipgloss.NewStyle().Foreground(p.ui.Accent).Background(pp.ground).Render("▎")
			if lead > 1 {
				mark += pad(pp, lead-1)
			}
		}

		text := pad(pp, tw)
		if i < len(lay.Lines) {
			text = pp.line(lay.Lines[i], lay.Links, tw)
		}

		rows = append(rows, mark+text+pad(pp, trail))
	}

	return rows
}
