# Plan: a link checker for the site builder

> For agentic workers: work through the steps in order and tick each box when its tests pass.

**Goal:** `gleam check` reads a built site and reports every broken internal link, with the page and line it came from.

**Architecture:**
- A walker lists the pages under the output folder
- A parser pulls every `href` and `src` out of each page
  - absolute URLs are skipped
  - fragments are checked against the page's own anchors
    - an anchor is any `id` attribute
    - a heading gets one from its text
- A reporter prints one line per broken link and sets the exit code

**Tech stack:** Go 1.27, `golang.org/x/net/html`, the standard library otherwise.

## File structure

| File                        | Job                                   |
| --------------------------- | ------------------------------------- |
| `internal/check/walk.go`    | list pages, never follow symlinks     |
| `internal/check/links.go`   | extract links and anchors from a page |
| `internal/check/report.go`  | format and count the findings         |

## Step 1: walk the output folder

- [ ] Write the failing test in `internal/check/walk_test.go`
  1. Build a folder with three pages and a symlink in `t.TempDir()`
  2. Assert the walker returns the three pages, sorted
  3. Run it and watch it fail:

     ```sh
     go test ./internal/check -run TestWalk
     ```

     Expected: `undefined: Walk`

- [ ] Implement `Walk` in `walk.go`

  ```go
  // Walk lists the .html files under root in lexical order.
  func Walk(ctx context.Context, root string) ([]string, error) {
  	var pages []string

  	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
  		if err != nil {
  			return fmt.Errorf("walk %s: %w", p, err)
  		}

  		if d.Type()&fs.ModeSymlink != 0 {
  			return nil
  		}

  		if strings.HasSuffix(p, ".html") {
  			pages = append(pages, p)
  		}

  		return ctx.Err()
  	})

  	return pages, err
  }
  ```

- [x] Commit: `feat(check): walk the output folder`

## Step 2: extract links

- [ ] Test the extractor on a page with every link shape
  - plain `<a href>` and `<img src>`
  - a link with a fragment only
  - a link with a query string and a fragment
    - the query is ignored when the file is looked up
    - the fragment is checked against the target page
      - a missing anchor is a finding, a missing file is another
- [ ] Implement it, then run `make check`
- [ ] Commit

## Step 3: report

| Finding        | Exit code | Example                      |
| :------------- | --------: | :--------------------------- |
| none           |         0 | `ok: 214 pages`              |
| broken link    |         1 | `a.html:12 -> missing.html`  |
| unreadable dir |         2 | `error: open dist: denied`   |

- [ ] Write the reporter and its golden test
- [ ] Wire `gleam check` into the CLI

> Keep the reporter free of colour: the CI log is plain text.

## Notes

1. Do not follow redirects, a redirect page is a finding too
2. Cap the walk at 100,000 pages
3. Never write into the output folder
