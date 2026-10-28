package md

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/mhersson/muxreveal/internal/git/invisible"
)

// resolved is a link or image target after the scheme and base rules: url for
// a target the UI may emit as OSC 8, path for a file against a local base.
// Both empty means the target is not followed.
type resolved struct{ url, path string }

// resolve applies the scheme and base rules to target. Only http, https and
// mailto targets give a url. A relative target resolves against the Doc's base:
// to a url against a URL base, to a path against a local base.
func (d *Doc) resolve(target string) resolved {
	target = strings.Trim(target, " \t\n\r\f")
	if target == "" || !cleanTarget(target) {
		return resolved{}
	}

	u, err := url.Parse(target)
	if err != nil {
		return resolved{}
	}

	if u.Scheme != "" {
		return linkable(u)
	}

	if strings.HasPrefix(target, "//") && u.Host == "" {
		return resolved{}
	}

	switch d.base.kind {
	case baseURL:
		return linkable(d.base.url.ResolveReference(u))
	case baseLocal:
		if u.Path == "" || u.Host != "" {
			return resolved{}
		}

		p := filepath.Join(d.base.dir, filepath.FromSlash(u.Path))
		if !cleanTarget(p) {
			return resolved{}
		}

		return resolved{path: p}
	}

	return resolved{}
}

// linkable keeps u when it is an http, https or mailto target.
func linkable(u *url.URL) resolved {
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Host == "" {
			return resolved{}
		}
	case "mailto":
		if u.Opaque == "" {
			return resolved{}
		}
	default:
		return resolved{}
	}

	s := u.String()
	if !cleanTarget(s) {
		return resolved{}
	}

	return resolved{url: s}
}

// cleanTarget reports that s holds no control character and no character that
// draws nothing, which would make it unsafe inside an OSC 8 sequence.
func cleanTarget(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == 0xfffd || invisible.Rune(r) {
			return false
		}
	}

	return true
}
