package md

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzLayout(f *testing.F) {
	paths, _ := filepath.Glob(filepath.Join("testdata", "corpus", "*.md"))
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			f.Fatal(err)
		}

		f.Add(src, uint8(40), true)
		f.Add(src, uint8(100), false)
	}

	hostile := hostileDoc()
	f.Add(hostile, uint8(30), true)
	f.Add(hostile, uint8(1), false)
	f.Add(hostile, uint8(100), true)

	for _, s := range []string{
		"", "<details>", "<details open><summary>", "```go\nunclosed", "|", "| a |\n|", "\xff\xfe\xc3\x28",
		"[a](", "![", ">>>>", "- [ ]", "[^1]: x", "<!--", "<details><details><details>",
	} {
		f.Add([]byte(s), uint8(10), true)
	}

	f.Fuzz(func(t *testing.T, src []byte, width uint8, icons bool) {
		d := Parse(src, ParseOptions{})
		o := LayoutOptions{Width: int(width), Icons: icons}
		limit := min(max(int(width), 1), maxWidth)

		for _, l := range []Layout{d.Layout(o), layoutAllOpen(d, o)} {
			assertSafe(t, l, limit)
			assertReferences(t, l, icons, limit)
			assertImageSrcs(t, l)

			for _, k := range l.Links {
				if !allowedScheme(k.URL) {
					t.Fatalf("link %q is outside the allowed schemes", k.URL)
				}
			}
		}
	})
}
