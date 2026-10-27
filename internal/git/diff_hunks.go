package git

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

// addHunks runs the hunk pass for the files that need hunks and fills
// files[i].Hunks. Files that exceed a cap, or that the byte cap cut off, keep
// their counts and are marked Truncated.
func (e *Engine) addHunks(ctx context.Context, r Repo, oldTree, newTree string, files []FileDiff) error {
	var need []int

	for i := range files {
		f := &files[i]
		if f.Binary || f.Added+f.Removed == 0 {
			continue
		}

		if i >= e.limits.Files || f.Added+f.Removed > e.limits.Lines {
			f.Truncated = true

			continue
		}

		if hunks, ok := e.cache.get(hunkKey{f.OldOID, f.NewOID}); ok {
			f.Hunks = hunks

			continue
		}

		need = append(need, i)
	}

	if len(need) == 0 {
		return nil
	}

	parsed, err := e.readHunks(ctx, r, oldTree, newTree, files, need)
	if err != nil {
		return err
	}

	for _, i := range need {
		f := &files[i]

		pf, ok := parsed[f.Path]
		if !ok || !sameBlobs(f, pf) {
			f.Truncated = true

			continue
		}

		for _, p := range pf {
			f.Hunks = append(f.Hunks, convertHunks(p)...)
		}

		e.cache.put(hunkKey{f.OldOID, f.NewOID}, f.Hunks)
	}

	return nil
}

// readHunks runs one git diff over the needed files, reads at most the byte
// cap, stops git, and returns the parsed files that were read completely,
// grouped by path (a type change is two patches for one path).
func (e *Engine) readHunks(ctx context.Context, r Repo, oldTree, newTree string, files []FileDiff, need []int) (map[string][]*gitdiff.File, error) {
	args := []string{
		"diff", "--histogram", "-U3", "-M", "-C", "--full-index", "--no-color", "--no-ext-diff",
		"--no-textconv", "--no-relative", "--submodule=short", "--ignore-submodules=none", "--inter-hunk-context=0", "--src-prefix=a/", "--dst-prefix=b/", oldTree, newTree, "--",
	}

	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			args = append(args, ":(literal)"+p)
		}
	}

	for _, i := range need {
		add(files[i].OldPath)
		add(files[i].Path)
	}

	// suppressBlankEmpty would print blank context lines without their space.
	rc, err := stream(ctx, call{dir: r.Root, args: args, read: true, env: []string{
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=diff.suppressBlankEmpty", "GIT_CONFIG_VALUE_0=false",
	}})
	if err != nil {
		return nil, fmt.Errorf("read hunks: %w", err)
	}

	buf, readErr := io.ReadAll(io.LimitReader(rc, int64(e.limits.Bytes)+1))
	closeErr := rc.Close() // kills git when the cap cut the output short

	overflow := len(buf) > e.limits.Bytes

	switch {
	case readErr != nil:
		return nil, fmt.Errorf("read hunks: %w", readErr)
	case ctx.Err() != nil:
		return nil, fmt.Errorf("read hunks: %w", ctx.Err())
	case closeErr != nil && !overflow:
		return nil, fmt.Errorf("read hunks: %w", closeErr)
	}

	if overflow {
		buf = wholeFiles(buf[:e.limits.Bytes])
	}

	parsed, _, err := gitdiff.Parse(bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("parse hunks: %w", err)
	}

	byPath := make(map[string][]*gitdiff.File, len(parsed))

	for _, pf := range parsed {
		name := pf.NewName
		if name == "" {
			name = pf.OldName
		}

		byPath[name] = append(byPath[name], pf)
	}

	return byPath, nil
}

// wholeFiles cuts buf before its last file header: the file it stopped in is
// incomplete. A complete file whose end coincides with the cap is dropped too,
// deliberately: it cannot be told from a cut one. A header line is unambiguous because content lines begin with
// ' ', '+', '-' or '\'.
func wholeFiles(buf []byte) []byte {
	i := bytes.LastIndex(buf, []byte("\ndiff --git "))
	if i < 0 {
		return nil
	}

	return buf[:i+1]
}

// sameBlobs reports whether the parsed patches cover exactly the blobs the
// listing named, so hunks never belong to a different pair than the counts.
func sameBlobs(f *FileDiff, parsed []*gitdiff.File) bool {
	first, last := parsed[0], parsed[len(parsed)-1]

	return oidOrEmpty(first.OldOIDPrefix) == f.OldOID && oidOrEmpty(last.NewOIDPrefix) == f.NewOID
}

func convertHunks(pf *gitdiff.File) []Hunk {
	hunks := make([]Hunk, 0, len(pf.TextFragments))

	for _, frag := range pf.TextFragments {
		h := Hunk{
			OldStart: int(frag.OldPosition), OldLines: int(frag.OldLines),
			NewStart: int(frag.NewPosition), NewLines: int(frag.NewLines),
			Section: frag.Comment,
			Lines:   make([]Line, 0, len(frag.Lines)),
		}
		oldN, newN := h.OldStart, h.NewStart

		for _, l := range frag.Lines {
			line := Line{Text: strings.TrimSuffix(l.Line, "\n"), NoNewline: l.NoEOL()}

			switch l.Op {
			case gitdiff.OpAdd:
				line.Kind, line.New = Add, newN
				newN++
			case gitdiff.OpDelete:
				line.Kind, line.Old = Del, oldN
				oldN++
			default:
				line.Kind, line.Old, line.New = Context, oldN, newN
				oldN++
				newN++
			}

			h.Lines = append(h.Lines, line)
		}

		hunks = append(hunks, h)
	}

	return hunks
}
