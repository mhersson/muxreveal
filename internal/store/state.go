// Package store reads, validates and writes the state file. The file is
// untrusted input: Decode never panics and nothing it returns needs escaping
// beyond what core already quotes.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/mhersson/muxreveal/internal/core"
)

// Version is the file format Encode writes and Decode reads without fallback.
const Version = 1

// ErrInvalid reports a state file whose tabs or panes cannot be used.
var ErrInvalid = errors.New("invalid state file")

const (
	maxFileSize   = 16 << 20
	maxNameRunes  = 32
	maxVersionLen = 16
	maxNumber     = 999_999_999 // keeps NextID + 1 far from overflow
	harnessClaude = "claude-code"
)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type fileJSON struct {
	Version json.RawMessage     `json:"version"`
	Active  int                 `json:"active"`
	Tabs    []tabJSON           `json:"tabs"`
	Panes   map[string]paneJSON `json:"panes"`
}

type tabJSON struct {
	ID      string          `json:"id"`
	Name    string          `json:"name,omitempty"`
	Focused string          `json:"focused"`
	Zoomed  bool            `json:"zoomed,omitempty"`
	PaneIDs []string        `json:"pane_ids"`
	Layout  json.RawMessage `json:"layout"`
}

type paneJSON struct {
	Kind       string `json:"kind"`
	Cwd        string `json:"cwd,omitempty"`
	Harness    string `json:"harness,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Transcript string `json:"transcript,omitempty"`
}

// Report says which fallbacks Decode used, for the start-up notices.
type Report struct {
	Version   string // "" for version 1; else the version as written, or "unknown" when missing
	Defaulted int    // version 1 tabs whose layout could not be read and got the default
}

// Encode returns the state file for st: indented, deterministic, ratios
// rounded to 0.01. Only panes that sit in a tab are written.
func Encode(st core.State) ([]byte, error) {
	tabs := st.Tabs
	if len(tabs) == 0 {
		tabs = []core.Tab{{ID: "t1"}}
	}
	if st.Active < 0 || st.Active >= len(tabs) {
		return nil, fmt.Errorf("active tab %d is out of range for %d tabs", st.Active, len(tabs))
	}
	f := fileJSON{
		Version: json.RawMessage(strconv.Itoa(Version)),
		Active:  st.Active,
		Panes:   map[string]paneJSON{},
	}
	for _, t := range tabs {
		layout, err := encodeLayout(t.Layout)
		if err != nil {
			return nil, fmt.Errorf("tab %s: %w", t.ID, err)
		}
		ids := leafOrder(t.Layout)
		jt := tabJSON{
			ID: string(t.ID), Name: t.Name, Focused: string(t.Focus), Zoomed: t.Zoomed,
			PaneIDs: make([]string, 0, len(ids)), Layout: layout,
		}
		for _, id := range ids {
			p, ok := st.Panes[id]
			if !ok {
				return nil, fmt.Errorf("tab %s: pane %s has no metadata", t.ID, id)
			}
			jp, err := encodePane(p)
			if err != nil {
				return nil, fmt.Errorf("pane %s: %w", id, err)
			}
			jt.PaneIDs = append(jt.PaneIDs, string(id))
			f.Panes[string(id)] = jp
		}
		f.Tabs = append(f.Tabs, jt)
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding state: %w", err)
	}
	return append(data, '\n'), nil
}

func encodePane(p core.Pane) (paneJSON, error) {
	switch p.Kind {
	case core.KindShell:
		return paneJSON{Kind: string(p.Kind), Cwd: p.Cwd}, nil
	case core.KindAgent:
		if p.Harness == "" {
			// Decode requires a harness; a file it would reject must never be written.
			return paneJSON{Kind: string(core.KindShell), Cwd: p.Cwd}, nil
		}
		return paneJSON{
			Kind: string(p.Kind), Cwd: p.Cwd, Harness: p.Harness,
			SessionID: string(p.Session), Transcript: p.Transcript,
		}, nil
	}
	return paneJSON{}, fmt.Errorf("unknown kind %q", p.Kind)
}

// Load reads and decodes the state file at path. A missing file wraps
// os.ErrNotExist.
func Load(path string) (core.State, Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return core.State{}, Report{}, fmt.Errorf("opening state file: %w", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return core.State{}, Report{}, fmt.Errorf("reading state file: %w", err)
	}
	return Decode(data)
}

// Decode parses and validates a state file. Tab and pane failures wrap
// ErrInvalid; a version other than 1 or a layout that cannot be read falls back
// to the default layout and is counted in the Report. Theme is left empty and
// every pane's State is AgentUnknown.
func Decode(data []byte) (core.State, Report, error) {
	invalid := func(format string, args ...any) (core.State, Report, error) {
		return core.State{}, Report{}, fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	if len(data) > maxFileSize {
		return invalid("file is larger than %d bytes", maxFileSize)
	}
	var f fileJSON
	if err := json.Unmarshal(data, &f); err != nil {
		return invalid("%v", err)
	}
	if len(f.Panes) > core.MaxPanes {
		return invalid("%d panes, at most %d", len(f.Panes), core.MaxPanes)
	}
	var rep Report
	v1 := isVersion1(f.Version)
	if !v1 {
		rep.Version = versionText(f.Version)
	}

	st := core.State{Panes: make(map[core.PaneID]core.Pane, len(f.Panes))}
	for key, jp := range f.Panes {
		n, err := parseNumbered(key, "p")
		if err != nil {
			return invalid("pane id %q: %v", key, err)
		}
		p, err := decodePane(jp)
		if err != nil {
			return invalid("pane %s: %v", key, err)
		}
		st.Panes[core.PaneID(key)] = p
		st.NextID = max(st.NextID, n)
	}

	if len(f.Tabs) == 0 {
		return invalid("no tabs")
	}
	if f.Active < 0 || f.Active >= len(f.Tabs) {
		return invalid("active tab %d is out of range for %d tabs", f.Active, len(f.Tabs))
	}
	st.Active = f.Active

	owner := map[core.PaneID]string{}
	tabIDs := map[string]bool{}
	for _, jt := range f.Tabs {
		n, err := parseNumbered(jt.ID, "t")
		if err != nil {
			return invalid("tab id %q: %v", jt.ID, err)
		}
		if tabIDs[jt.ID] {
			return invalid("tab %s appears twice", jt.ID)
		}
		tabIDs[jt.ID] = true
		st.NextTabID = max(st.NextTabID, n)

		ids := make([]core.PaneID, 0, len(jt.PaneIDs))
		for _, s := range jt.PaneIDs {
			id := core.PaneID(s)
			if _, ok := st.Panes[id]; !ok {
				return invalid("tab %s names unknown pane %q", jt.ID, s)
			}
			if o, dup := owner[id]; dup {
				return invalid("pane %s is in tab %s and tab %s", s, o, jt.ID)
			}
			owner[id] = jt.ID
			ids = append(ids, id)
		}
		if len(ids) == 0 && len(f.Tabs) > 1 {
			return invalid("tab %s has no panes", jt.ID)
		}

		var layout *core.Node
		if v1 {
			layout, err = decodeLayout(jt.Layout, ids)
			if err != nil {
				layout = defaultLayout(ids)
				rep.Defaulted++
			}
		} else {
			layout = defaultLayout(ids)
		}
		focus := core.PaneID(jt.Focused)
		if order := leafOrder(layout); len(order) == 0 {
			focus = ""
		} else if owner[focus] != jt.ID {
			focus = order[0]
		}
		st.Tabs = append(st.Tabs, core.Tab{
			ID:     core.TabID(jt.ID),
			Name:   cleanName(jt.Name),
			Layout: layout,
			Focus:  focus,
			Zoomed: jt.Zoomed && focus != "",
		})
	}
	for id := range st.Panes {
		if _, ok := owner[id]; !ok {
			return invalid("pane %s is in no tab", id)
		}
	}
	return st, rep, nil
}

func decodePane(jp paneJSON) (core.Pane, error) {
	p := core.Pane{Kind: core.PaneKind(jp.Kind)}
	if filepath.IsAbs(jp.Cwd) {
		p.Cwd = jp.Cwd
	}
	switch p.Kind {
	case core.KindShell:
	case core.KindAgent:
		if jp.Harness != harnessClaude {
			return core.Pane{}, fmt.Errorf("unknown harness %q", jp.Harness)
		}
		p.Harness = jp.Harness
		if uuidRE.MatchString(jp.SessionID) {
			p.Session = core.SessionID(jp.SessionID)
			p.Transcript = jp.Transcript
		}
	default:
		return core.Pane{}, fmt.Errorf("unknown kind %q", jp.Kind)
	}
	return p, nil
}

// parseNumbered reads ids such as "p12": the prefix, then a positive number
// without leading zeros.
func parseNumbered(s, prefix string) (int, error) {
	digits, ok := strings.CutPrefix(s, prefix)
	if !ok || digits == "" || digits[0] == '0' || len(digits) > 9 {
		return 0, fmt.Errorf("want %s followed by a positive number", prefix)
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("want %s followed by a positive number", prefix)
		}
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n > maxNumber {
		return 0, errors.New("number is too large")
	}
	return n, nil
}

func isVersion1(raw json.RawMessage) bool {
	var v float64
	return json.Unmarshal(raw, &v) == nil && v == Version
}

func isNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null"))
}

// versionText is the version as written, cut and cleaned because it reaches
// the status bar.
func versionText(raw json.RawMessage) string {
	if isNull(raw) {
		return "unknown"
	}
	return cleanText(string(raw), maxVersionLen)
}

func cleanName(s string) string { return cleanText(s, maxNameRunes) }

// cleanText drops control characters and invalid UTF-8, then cuts to max runes.
func cleanText(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, ""))
	if r := []rune(s); len(r) > limit {
		return string(r[:limit])
	}
	return s
}
