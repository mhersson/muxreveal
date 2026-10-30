package md

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fuzzBases are the bases FuzzLayout picks from: none, a URL and a local directory.
var fuzzBases = [...]string{"", "https://example.test/docs/guide/", "/tmp/muxreveal-fuzz/docs"}

func FuzzLayout(f *testing.F) {
	paths, _ := filepath.Glob(filepath.Join("testdata", "corpus", "*.md"))
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			f.Fatal(err)
		}

		f.Add(src, uint8(40), true, uint8(0))
		f.Add(src, uint8(100), false, uint8(0))
		f.Add(src, uint8(80), true, uint8(1))
		f.Add(src, uint8(60), false, uint8(2))
	}

	hostile := hostileDoc()
	f.Add(hostile, uint8(30), true, uint8(0))
	f.Add(hostile, uint8(1), false, uint8(1))
	f.Add(hostile, uint8(100), true, uint8(2))

	for _, s := range []string{
		"", "<details>", "<details open><summary>", "```go\nunclosed", "|", "| a |\n|", "\xff\xfe\xc3\x28",
		"[a](", "![", ">>>>", "- [ ]", "[^1]: x", "<!--", "<details><details><details>",
	} {
		f.Add([]byte(s), uint8(10), true, uint8(0))
	}

	// Nesting past the depth where the layout stops recursing, so the fuzz starts on the flat path.
	for _, depth := range []int{34, 40, 66} {
		deep := []byte(mixedNesting(depth))
		f.Add(deep, uint8(100), true, uint8(0))
		f.Add(deep, uint8(24), false, uint8(1))
	}

	f.Add([]byte(strings.Repeat("> - ", 20)+"deep"), uint8(80), true, uint8(0))
	f.Add([]byte(strings.Repeat("<details open>\n", 36)+"deep"), uint8(50), true, uint8(2))

	for _, s := range []string{
		"[a](../../x) ![b](../y.png) [c](/abs) [d](#frag) [e](//host/z) ![f](a%2fb.png) [g](..%2f..%2fx)",
		"<img src=\"../../a.png\"> <a href=\"../b\">x</a>",
	} {
		f.Add([]byte(s), uint8(40), true, uint8(1))
		f.Add([]byte(s), uint8(40), true, uint8(2))
	}

	f.Fuzz(func(t *testing.T, src []byte, width uint8, icons bool, base uint8) {
		d := Parse(src, ParseOptions{Base: fuzzBases[int(base)%len(fuzzBases)]})
		o := LayoutOptions{Width: int(width), Icons: icons}
		limit := min(max(int(width), 1), maxWidth)

		for _, l := range []Layout{d.Layout(o), layoutAllOpen(d, o)} {
			assertSafe(t, l, limit)
			assertReferences(t, l, icons, limit)
			assertImageSrcs(t, l)
			assertSource(t, l, len(d.starts))

			for _, k := range l.Links {
				if !allowedScheme(k.URL) {
					t.Fatalf("link %q is outside the allowed schemes", k.URL)
				}
			}

			for _, line := range l.Lines {
				for _, r := range line {
					if r.Path != "" && (!filepath.IsAbs(r.Path) || filepath.Clean(r.Path) != r.Path) {
						t.Fatalf("path %q is not a clean absolute path", r.Path)
					}
				}
			}
		}
	})
}
