package github

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	templateFile  = "pull_request_template.md"
	templateDir   = "pull_request_template"
	maxTemplateSz = 64 << 10
)

// Template is a pull request template found in a worktree.
type Template struct{ Name, Path, Body string }

// Commit is the part of a commit that fills a pull request.
type Commit struct{ Subject, Body string }

// Draft is the title and body a new pull request starts with.
type Draft struct{ Title, Body string }

// Templates returns the pull request templates under root: the single-file
// ones in the root, .github and docs, then the files of each PULL_REQUEST_TEMPLATE
// directory there, by name. Every read goes through os.OpenRoot, so a symlink
// that leaves root is skipped. A missing location, a symlink that leaves root
// or dangles, a non-regular file and a file over 64 KiB are skipped. Any other
// read error is joined into the returned error, and the templates that could
// be read are returned with it.
func Templates(root string) ([]Template, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", root, err)
	}
	defer r.Close()

	var (
		found []Template
		errs  []error
		dirs  []string
	)

	top, err := entries(r, ".")
	if err != nil {
		errs = append(errs, err)
	}

	locations := []string{"."}
	locations = append(locations, matching(top, ".github")...)
	locations = append(locations, matching(top, "docs")...)

	add := func(rel, name string) {
		t, ok, err := readTemplate(r, rel, name)
		if err != nil {
			errs = append(errs, err)
		}

		if ok {
			found = append(found, t)
		}
	}

	for _, loc := range locations {
		names := top

		if loc != "." {
			var err error

			if names, err = entries(r, loc); err != nil {
				errs = append(errs, err)
			}
		}

		for _, name := range matching(names, templateFile) {
			add(path.Join(loc, name), name)
		}

		for _, name := range matching(names, templateDir) {
			dirs = append(dirs, path.Join(loc, name))
		}
	}

	for _, dir := range dirs {
		names, err := entries(r, dir)
		if err != nil {
			errs = append(errs, err)
		}

		for _, name := range names {
			if strings.EqualFold(path.Ext(name), ".md") {
				add(path.Join(dir, name), name)
			}
		}
	}

	return found, errors.Join(errs...)
}

// stat follows a symlink only when it stays inside the root; one that escapes
// or dangles reports ok=false, as does a missing path.
func stat(r *os.Root, rel string) (fs.FileInfo, bool, error) {
	fi, err := r.Lstat(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("stat %s: %w", rel, err)
	}

	if fi.Mode()&fs.ModeSymlink == 0 {
		return fi, true, nil
	}

	fi, err = r.Stat(rel)
	if err != nil {
		return nil, false, nil //nolint:nilerr // escaping and dangling links are skipped
	}

	return fi, true, nil
}

// entries lists the sorted names in the directory rel, or nothing when rel is
// not a readable directory inside the root.
func entries(r *os.Root, rel string) ([]string, error) {
	fi, ok, err := stat(r, rel)
	if err != nil || !ok || !fi.IsDir() {
		return nil, err
	}

	d, err := r.Open(rel)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", rel, err)
	}
	defer d.Close()

	list, err := d.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", rel, err)
	}

	names := make([]string, 0, len(list))
	for _, e := range list {
		names = append(names, e.Name())
	}

	slices.Sort(names)

	return names, nil
}

func matching(names []string, want string) []string {
	var out []string

	for _, n := range names {
		if strings.EqualFold(n, want) {
			out = append(out, n)
		}
	}

	return out
}

func readTemplate(r *os.Root, rel, name string) (Template, bool, error) {
	fi, ok, err := stat(r, rel)
	if err != nil || !ok || !fi.Mode().IsRegular() || fi.Size() > maxTemplateSz {
		return Template{}, false, err
	}

	f, err := r.Open(rel)
	if err != nil {
		return Template{}, false, fmt.Errorf("open %s: %w", rel, err)
	}
	defer f.Close()

	// The size may have grown since the stat; read one byte past the limit.
	body, err := io.ReadAll(io.LimitReader(f, maxTemplateSz+1))
	if err != nil {
		return Template{}, false, fmt.Errorf("read %s: %w", rel, err)
	}

	if len(body) > maxTemplateSz {
		return Template{}, false, nil
	}

	return Template{Name: name, Path: rel, Body: string(body)}, true, nil
}

// Fill returns the defaults of `gh pr create --fill`: the only commit's subject
// as the title, else the branch name made readable; a non-blank template as the
// body, else the only commit's body or one "- subject" line per commit.
func Fill(branch string, commits []Commit, template string) Draft {
	var d Draft

	if len(commits) == 1 {
		d.Title = commits[0].Subject
	} else {
		d.Title = branchTitle(branch)
	}

	switch {
	case strings.TrimSpace(template) != "":
		d.Body = template
	case len(commits) == 1:
		d.Body = commits[0].Body
	case len(commits) > 1:
		var b strings.Builder
		for _, c := range commits {
			b.WriteString("- " + c.Subject + "\n")
		}

		d.Body = b.String()
	}

	return d
}

func branchTitle(branch string) string {
	s := strings.NewReplacer("-", " ", "_", " ").Replace(branch)

	first, size := utf8.DecodeRuneInString(s)
	if size == 0 || first == utf8.RuneError {
		return s
	}

	return string(unicode.ToUpper(first)) + s[size:]
}
