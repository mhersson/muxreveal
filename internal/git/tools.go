package git

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Tool is a development tool in the main checkout that would pick up files
// under .worktrees/ unless it gets its own ignore entry.
type Tool struct {
	Name string
	Path string // the file or directory that triggered it, relative to the root
	Hint string // what to add
}

// DetectTools reads files in root, never runs anything, and lists the tools
// that need an ignore entry for .worktrees. JSON that does not parse counts as
// needing an entry. A check that fails to read is skipped and joined into the
// error, so the tools found are still returned.
func DetectTools(root string) ([]Tool, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("detect tools in %s: %w", root, err)
	}

	d := detector{root: root}

	for _, e := range entries {
		if e.IsDir() {
			d.dirs = append(d.dirs, e.Name())

			continue
		}

		d.names = append(d.names, e.Name())
	}

	checks := []func() (*Tool, error){
		d.jetbrains, d.vscode, d.jestVitest, d.pytest, d.typescript, d.eslint, d.docker,
	}

	var (
		tools []Tool
		errs  []error
	)

	for _, check := range checks {
		tool, err := check()
		if err != nil {
			errs = append(errs, err)

			continue
		}

		if tool != nil {
			tools = append(tools, *tool)
		}
	}

	if len(errs) > 0 {
		return tools, fmt.Errorf("detect tools in %s: %w", root, errors.Join(errs...))
	}

	return tools, nil
}

type detector struct {
	root  string
	names []string // entries of root, sorted
	dirs  []string
}

func (d detector) has(name string) bool {
	for _, n := range d.names {
		if n == name {
			return true
		}
	}

	return false
}

// match returns the first root entry whose name matches the glob pattern.
func (d detector) match(pattern string) (string, bool) {
	for _, n := range d.names {
		if ok, _ := filepath.Match(pattern, n); ok {
			return n, true
		}
	}

	return "", false
}

// read returns the file's content; ok is false when it does not exist.
func (d detector) read(rel string) (content string, ok bool, err error) {
	b, err := os.ReadFile(filepath.Join(d.root, rel))
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.EISDIR) {
		return "", false, nil
	}

	if err != nil {
		return "", false, err
	}

	return string(b), true, nil
}

func hasLine(content, line string) bool {
	for l := range strings.SplitSeq(content, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}

	return false
}

func (d detector) jetbrains() (*Tool, error) {
	for _, n := range d.dirs {
		if n == ".idea" {
			return &Tool{"JetBrains", ".idea", `mark ".worktrees" as excluded in the project structure`}, nil
		}
	}

	return nil, nil
}

func (d detector) vscode() (*Tool, error) {
	const rel = ".vscode/settings.json"

	content, ok, err := d.read(rel)
	if err != nil || !ok {
		return nil, err
	}

	tool := &Tool{"VS Code", rel, `add ".worktrees" to files.watcherExclude`}

	var settings struct {
		Watcher map[string]json.RawMessage `json:"files.watcherExclude"`
	}

	if json.Unmarshal([]byte(content), &settings) != nil {
		return tool, nil
	}

	for key := range settings.Watcher {
		if strings.Contains(key, ".worktrees") {
			return nil, nil
		}
	}

	return tool, nil
}

func (d detector) jestVitest() (*Tool, error) {
	const hint = `add ".worktrees" to the test config's ignore patterns`

	if n, ok := d.match("jest.config.*"); ok {
		return &Tool{"Jest", n, hint}, nil
	}

	if n, ok := d.match("vitest.config.*"); ok {
		return &Tool{"Vitest", n, hint}, nil
	}

	content, ok, err := d.read("package.json")
	if err != nil || !ok {
		return nil, err
	}

	var pkg struct {
		Jest json.RawMessage `json:"jest"`
	}

	if json.Unmarshal([]byte(content), &pkg) == nil && pkg.Jest != nil {
		return &Tool{"Jest", "package.json", hint}, nil
	}

	return nil, nil
}

func (d detector) pytest() (*Tool, error) {
	files := []struct{ path, section string }{
		{"pytest.ini", ""},
		{"pyproject.toml", "[tool.pytest.ini_options]"},
		{"setup.cfg", "[tool:pytest]"},
		{"tox.ini", "[pytest]"},
	}

	for _, f := range files {
		content, ok, err := d.read(f.path)
		if err != nil {
			return nil, err
		}

		if ok && (f.section == "" || hasLine(content, f.section)) {
			return &Tool{"pytest", f.path, `add ".worktrees" to norecursedirs`}, nil
		}
	}

	return nil, nil
}

func (d detector) typescript() (*Tool, error) {
	const rel = "tsconfig.json"

	content, ok, err := d.read(rel)
	if err != nil || !ok {
		return nil, err
	}

	tool := &Tool{"TypeScript", rel, `add ".worktrees" to exclude, or narrow include`}

	var cfg struct {
		Include []string `json:"include"`
	}

	if json.Unmarshal([]byte(content), &cfg) != nil || cfg.Include == nil {
		return tool, nil
	}

	for _, inc := range cfg.Include {
		if strings.HasPrefix(inc, "**") {
			return tool, nil
		}
	}

	return nil, nil
}

func (d detector) eslint() (*Tool, error) {
	for _, pattern := range []string{".eslintrc*", "eslint.config.*"} {
		if n, ok := d.match(pattern); ok {
			return &Tool{"ESLint", n, `add ".worktrees" to the ignore patterns`}, nil
		}
	}

	return nil, nil
}

func (d detector) docker() (*Tool, error) {
	if !d.has("Dockerfile") {
		return nil, nil
	}

	hint := `add ".worktrees" to .dockerignore`

	content, ok, err := d.read(".dockerignore")
	if err != nil {
		return nil, err
	}

	if !ok {
		return &Tool{"Docker", "Dockerfile", hint}, nil
	}

	for l := range strings.SplitSeq(content, "\n") {
		if strings.Contains(l, ".worktrees") {
			return nil, nil
		}
	}

	return &Tool{"Docker", ".dockerignore", hint}, nil
}
