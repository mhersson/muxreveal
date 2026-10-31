package ui

import (
	"slices"
	"sort"
	"strings"

	"github.com/mhersson/muxreveal/internal/md"
)

// changeMark is the mark a rendered line carries in the drawer's first cell.
type changeMark uint8

// Change marks.
const (
	markNone changeMark = iota
	markAdded
	markRemoved
)

// mdRows is what a rendered file lays out as: the layout and the mark of each
// of its lines.
type mdRows struct {
	lay   md.Layout
	marks []changeMark
}

// mdFile is a parsed markdown file with the two texts it was read from, so
// that its marks never mix two versions.
type mdFile struct {
	doc  *mdDoc
	text fullText
	ids  blobIDs

	rowsGen uint64 // the picture generation the rows were last laid out at
}

// changeMarks marks the lines of a layout whose source ranges are src, by the
// edit script of ft, whose new side the layout was made from. A block is a run
// of consecutive lines with one non-zero range. A block holding an added line
// is marked on every line; else a block that removed lines sit before, after
// the previous block's last line up to its own, is marked on its first line.
// Lines removed after the last block and removed blank lines between blocks
// mark nothing.
func changeMarks(src []md.SourceRange, ft fullText) []changeMark {
	out := make([]changeMark, len(src))
	nw, ops := ft.new, ft.ops

	// added[n] counts the added lines among new lines 1..n.
	added := make([]int, len(nw)+1)

	// before holds, for each removal, the new line it sits before.
	var before []int

	// blankBefore is the same for removals of blank lines only. Between two
	// blocks they change nothing drawn; inside one they can.
	var blankBefore []int

	ni := 0

	for i := 0; i < len(ops); {
		switch ops[i].Op {
		case '+':
			ni++
			added[ni] = added[ni-1] + 1
			i++
		case '-':
			blank := true

			for i < len(ops) && ops[i].Op == '-' {
				blank = blank && strings.TrimSpace(ops[i].Line) == ""
				i++
			}

			if blank {
				blankBefore = append(blankBefore, ni+1)
			} else {
				before = append(before, ni+1)
			}
		default:
			ni++
			added[ni] = added[ni-1]
			i++
		}
	}

	for n := ni + 1; n < len(added); n++ {
		added[n] = added[n-1]
	}

	slices.Sort(before)
	slices.Sort(blankBefore)

	prevLast := 0

	for i := 0; i < len(src); {
		r := src[i]
		j := i + 1

		for j < len(src) && src[j] == r {
			j++
		}

		if r.First != 0 {
			first, last := min(r.First, len(nw)), min(r.Last, len(nw))

			switch {
			case first >= 1 && last >= first && added[last]-added[first-1] > 0:
				for k := i; k < j; k++ {
					out[k] = markAdded
				}
			default:
				// The first removal sitting before a line after prevLast.
				if removedBefore(before, prevLast+1, r.Last) || removedBefore(blankBefore, max(prevLast, r.First)+1, r.Last) {
					out[i] = markRemoved
				}
			}

			prevLast = max(prevLast, r.Last)
		}

		i = j
	}

	return out
}

// removedBefore reports whether the sorted positions hold one in [from, to].
func removedBefore(at []int, from, to int) bool {
	k := sort.SearchInts(at, from)

	return k < len(at) && at[k] <= to
}
