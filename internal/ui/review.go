package ui

import (
	"math"
	"time"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/ui/diffview"
)

const (
	// slideFor is how long the drawer takes to slide in or out.
	slideFor = 120 * time.Millisecond
	// drawerMinW is the narrowest the drawer is, unless the body is narrower.
	drawerMinW = 60
	// splitMinCode is the least code columns each side of a side by side row needs.
	splitMinCode = 24
	// treeMinInner is the inner width from which the tree always shows.
	treeMinInner = 80
	// hintsMinInner is the inner height from which the key hints show.
	hintsMinInner = 12
)

// reviewView is one tab's drawer content: the loaded diff, its rows and where
// the cursor is. Only the update loop touches it.
type reviewView struct {
	src  git.Source
	root string
	//nolint:unused // the loader sets it with root
	session     string
	res         git.Result
	gen         int // bumps whenever res is replaced; the caches follow it
	rows        []reviewRow
	cursor, top int
	split       bool // side by side is wanted; rowsSplit says whether the rows are laid out so
	treeOpen    bool // a drawer too narrow for the tree shows it
	full        map[string]fullText
	folded      map[string]bool
	flashUntil  time.Time
	fresh       map[int]bool
	search      *string // the open search prompt's text; nil when closed
	last        string  // the last search
	err         error

	// What the title, the mode tag, the mode row and the info rows show and
	// git.Result does not carry.
	project, branch string
	base            string // the base the branch is compared with, resolved
	turns           int    // how many turns the session has
	noSnapshot      bool   // the turn shown has no snapshot

	rowsSplit bool // rows hold side by side pairs
	treeSel   int  // the file selected while the tree has the keyboard

	cache reviewCache
}

// reviewCache holds what is derived from a result once per generation.
type reviewCache struct {
	ok     bool
	gen    int
	files  int
	tree   []treeItem
	hidden []bool // per file: its paths or text carry a hidden character
	digits int    // digits of the highest line number in the hunks
	spans  map[spanKey][][]diffview.Span
}

// drawerState is the drawer's UI state outside the views.
type drawerState struct {
	focused, tree bool // the drawer has the keyboard; within it, the tree has
	slideFrom     time.Time
	closing       bool
	views         map[core.TabID]*reviewView
	hl            diffview.Highlighter
	dv            *diffview.Theme // the diff view's theme for dvName
	dvName        string
	dvFlash       *diffview.Theme // the same on the flash ground
	tokenised     int             // lines tokenised by the last highlighting pass
}

// reviewViewOf returns the tab's view, creating it.
func (a *App) reviewViewOf(id core.TabID) *reviewView {
	if a.drawer.views == nil {
		a.drawer.views = map[core.TabID]*reviewView{}
	}

	v, ok := a.drawer.views[id]
	if !ok {
		v = &reviewView{}
		a.drawer.views[id] = v
	}

	return v
}

// derived is the view's cache, rebuilt when the result changed.
func (v *reviewView) derived() *reviewCache {
	c := &v.cache
	if c.ok && c.gen == v.gen && c.files == len(v.res.Files) {
		return c
	}

	*c = reviewCache{ok: true, gen: v.gen, files: len(v.res.Files), spans: map[spanKey][][]diffview.Span{}}
	c.tree = treeItems(v.res.Files)
	c.hidden = make([]bool, len(v.res.Files))

	hi := 0

	for i, f := range v.res.Files {
		c.hidden[i] = fileHidden(f)

		for _, h := range f.Hunks {
			hi = max(hi, h.OldStart+h.OldLines, h.NewStart+h.NewLines)
		}
	}

	c.digits = numDigits(hi)

	return c
}

func numDigits(n int) int {
	d := 1
	for ; n >= 10; n /= 10 {
		d++
	}

	return d
}

// rebuild lays the rows out again, for side by side or not, and keeps the
// cursor on what it was on.
func (v *reviewView) rebuild(split bool) {
	anchor := anchorAt(v.res, v.rows, v.cursor)

	v.rowsSplit = split
	v.rows = buildRows(v.res, rowOpts{split: split, full: v.full, folded: v.folded})
	v.cursor = restoreAnchor(v.res, v.rows, anchor)
}

// easeOutCubic maps the slide's progress to how far the drawer has moved.
func easeOutCubic(t float64) float64 {
	u := 1 - t

	return 1 - u*u*u
}

// slideEase is how far along the slide is, 0 to 1, eased; 1 when none runs.
func (a *App) slideEase() float64 {
	if a.drawer.slideFrom.IsZero() {
		return 1
	}

	t := float64(a.clock.Now().Sub(a.drawer.slideFrom)) / float64(slideFor)

	return easeOutCubic(min(max(t, 0), 1))
}

// drawerFull is the active tab's drawer once it has slid in: the right half of
// the body, at least 60 columns, full height. ok is false with no drawer open
// or a body too small for a frame.
func (a *App) drawerFull(st core.State) (core.Rect, bool) {
	if st.Tab().Review == nil {
		return core.Rect{}, false
	}

	body := a.bodyAreaIn(st.Rail)
	if body.W == 0 {
		return core.Rect{}, false
	}

	w := max(body.W-body.W/2, min(body.W, drawerMinW))

	return core.Rect{X: body.X + body.W - w, Y: body.Y, W: w, H: body.H}, true
}

// drawerRect is the part of the drawer on screen: all of it when it has slid
// in, and less while it slides, from the right edge.
func (a *App) drawerRect(st core.State) (core.Rect, bool) {
	full, ok := a.drawerFull(st)
	if !ok {
		return core.Rect{}, false
	}

	frac := a.slideEase()
	if a.drawer.closing {
		frac = 1 - frac
	}

	shown := int(math.Round(frac * float64(full.W)))
	if shown <= 0 {
		return core.Rect{}, false
	}

	full.X += full.W - shown
	full.W = shown

	return full, true
}

// drawerKeys reports whether the drawer is on screen and has the keyboard.
func (a *App) drawerKeys(st core.State) bool {
	if !a.drawer.focused {
		return false
	}

	_, ok := a.drawerRect(st)

	return ok
}
