// Adapted from go-diff's diffBisect in diffmatchpatch/diff.go at v1.4.0
// (github.com/sergi/go-diff). Copyright (c) 2012-2016 The go-diff authors. MIT
// License. go-diff is a Go port of Google's diff-match-patch, Copyright (c) 2006
// Google Inc., Apache License 2.0. See THIRD_PARTY.md. Changed from it: split
// compares lines, numbered so that they compare as integers, instead of runes,
// and gives up at a work cap instead of a deadline.

package linediff

import "slices"

// maxWork caps Diff: the lines between the common start and end, both sides
// counted, times the lines that change.
const maxWork = 1 << 30

// Diff is a shortest edit script from a to b, by Myers' O(ND) algorithm in
// linear space. The lines both start and end with are kept, and the ones
// between are compared while their count times the lines that change stays
// at most 1<<30; past that they are Replace. In each run of changes the
// removed lines come before the added ones, as git prints them.
func Diff(a, b []string) []Op { return diff(a, b, maxWork) }

func diff(a, b []string, work int) []Op {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}

	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}

	if len(a)+len(b) == 0 {
		return nil
	}

	ops := make([]Op, 0, len(a)+len(b)-pre-suf)
	for _, l := range a[:pre] {
		ops = append(ops, Op{' ', l})
	}

	ops = myers(ops, a[pre:len(a)-suf], b[pre:len(b)-suf], work)

	for _, l := range a[len(a)-suf:] {
		ops = append(ops, Op{' ', l})
	}

	removedFirst(ops)

	return ops
}

// myers appends the script from a to b to ops: a shortest one while its edit
// distance d keeps (len(a)+len(b))*d at most work, else Replace's. The first
// and the last lines of a and b differ.
func myers(ops []Op, a, b []string, work int) []Op {
	n, m := len(a), len(b)

	// The edit distance is at least the difference in length.
	if n == 0 || m == 0 || (n+m)*max(n-m, m-n) > work {
		return appendReplace(ops, a, b)
	}

	ids := make(map[string]int, n+m)
	number := func(lines []string) []int {
		out := make([]int, len(lines))
		for i, l := range lines {
			id, ok := ids[l]
			if !ok {
				id = len(ids)
				ids[l] = id
			}

			out[i] = id
		}

		return out
	}

	size := n + m + 4
	d := &differ{a: number(a), b: number(b), la: a, lb: b, ops: ops, vf: make([]int, size), vb: make([]int, size)}

	x, y, ok := d.split(0, n, 0, m, work/(n+m))
	if !ok {
		return appendReplace(ops, a, b)
	}

	d.halves(0, n, 0, m, x, y)

	return d.ops
}

// appendReplace appends Replace's script from a to b to ops.
func appendReplace(ops []Op, a, b []string) []Op {
	for _, l := range a {
		ops = append(ops, Op{'-', l})
	}

	for _, l := range b {
		ops = append(ops, Op{'+', l})
	}

	return ops
}

// differ holds both texts as line ids, the script so far and the scratch
// rows of the furthest points reached on each diagonal.
type differ struct {
	a, b   []int
	la, lb []string
	ops    []Op
	vf, vb []int
}

// compare appends the script from a[a0:a1] to b[b0:b1].
func (d *differ) compare(a0, a1, b0, b1 int) {
	for a0 < a1 && b0 < b1 && d.a[a0] == d.b[b0] {
		d.ops = append(d.ops, Op{' ', d.la[a0]})
		a0++
		b0++
	}

	end := a1
	for a0 < a1 && b0 < b1 && d.a[a1-1] == d.b[b1-1] {
		a1--
		b1--
	}

	switch {
	case a0 == a1 || b0 == b1:
		d.replace(a0, a1, b0, b1)
	default:
		x, y, _ := d.split(a0, a1, b0, b1, -1)
		d.halves(a0, a1, b0, b1, x, y)
	}

	for ; a1 < end; a1++ {
		d.ops = append(d.ops, Op{' ', d.la[a1]})
	}
}

// halves appends the script through the split point x, y, which is -1 for two
// ranges with no line in common.
func (d *differ) halves(a0, a1, b0, b1, x, y int) {
	if x < 0 {
		d.replace(a0, a1, b0, b1)

		return
	}

	d.compare(a0, x, b0, y)
	d.compare(x, a1, y, b1)
}

func (d *differ) replace(a0, a1, b0, b1 int) {
	d.ops = appendReplace(d.ops, d.la[a0:a1], d.lb[b0:b1])
}

// split finds x, y on a shortest path from a[a0:a1] to b[b0:b1], with each
// side of it a shorter problem, by searching forward and back from the ends
// at once until the two meet (Myers' middle snake). Both ranges are non-empty
// and their first and last lines differ. It returns -1, -1 for ranges with no
// line in common, and false once the edit distance must pass limit, which is
// -1 for none.
func (d *differ) split(a0, a1, b0, b1, limit int) (x, y int, ok bool) {
	n, m := a1-a0, b1-b0
	steps := (n + m + 1) / 2
	off := steps + 1
	vf, vb := d.vf[:2*off+1], d.vb[:2*off+1]

	for i := range vf {
		vf[i], vb[i] = -1, -1
	}

	vf[off+1], vb[off+1] = 0, 0
	delta := n - m
	odd := delta%2 != 0

	// The diagonals at either end that ran off the grid are skipped.
	var fStart, fEnd, bStart, bEnd int

	for s := range steps {
		// A meeting at step s has edit distance 2s-1 when delta is odd, else 2s.
		if least := 2 * s; limit >= 0 && (odd && max(least-1, 1) > limit || !odd && least > limit) {
			return 0, 0, false
		}

		for k := -s + fStart; k <= s-fEnd; k += 2 {
			i := off + k

			px := vf[i-1] + 1
			if k == -s || (k != s && vf[i-1] < vf[i+1]) {
				px = vf[i+1]
			}

			py := px - k
			for px < n && py < m && d.a[a0+px] == d.b[b0+py] {
				px++
				py++
			}

			vf[i] = px

			switch {
			case px > n:
				fEnd += 2
			case py > m:
				fStart += 2
			case odd:
				// The backward search's diagonal delta-k counts from the ends.
				if j := off + delta - k; j >= 0 && j < len(vb) && vb[j] != -1 && px >= n-vb[j] {
					return a0 + px, b0 + py, true
				}
			}
		}

		for k := -s + bStart; k <= s-bEnd; k += 2 {
			i := off + k

			px := vb[i-1] + 1
			if k == -s || (k != s && vb[i-1] < vb[i+1]) {
				px = vb[i+1]
			}

			py := px - k
			for px < n && py < m && d.a[a1-1-px] == d.b[b1-1-py] {
				px++
				py++
			}

			vb[i] = px

			switch {
			case px > n:
				bEnd += 2
			case py > m:
				bStart += 2
			case !odd:
				if j := off + delta - k; j >= 0 && j < len(vf) && vf[j] != -1 && vf[j] >= n-px {
					fx := vf[j]

					return a0 + fx, b0 + fx - (j - off), true
				}
			}
		}
	}

	return -1, -1, true
}

// removedFirst puts the removed lines of each run of changes before its added
// ones.
func removedFirst(ops []Op) {
	for i := 0; i < len(ops); {
		if ops[i].Op == ' ' {
			i++

			continue
		}

		j := i
		for j < len(ops) && ops[j].Op != ' ' {
			j++
		}

		slices.SortStableFunc(ops[i:j], func(x, y Op) int { return added(x) - added(y) })
		i = j
	}
}

func added(o Op) int {
	if o.Op == '+' {
		return 1
	}

	return 0
}
