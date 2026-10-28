# Third-party material

MuxReveal includes material copied from the projects below, each under the MIT License (text at the end).

## Catppuccin palette

- Source: https://github.com/catppuccin/palette
- Copyright (c) 2021 Catppuccin
- Used in `internal/ui/theme/palette.go`: the colour values of the Latte, Frappé, Macchiato and Mocha flavors.

## Chroma Catppuccin styles

- Source: https://github.com/alecthomas/chroma (`styles/catppuccin-*.xml`, v2.27.0)
- Copyright (C) 2017 Alec Thomas
- Used in `internal/ui/theme/syntax.go`: the mapping from token types to colours and text attributes.

## tuios line diff and invisible characters

- Source: https://github.com/Gaurav-Gosain/tuios (`internal/linediff/linediff.go`, `internal/invisible/invisible.go`, `internal/invisible/invisible_test.go`) at commit aaaa128cd35b38ba8aa08a5f69e688f01385ec3e
- Copyright (c) 2025 Gaurav Gosain
- Used in `internal/git/linediff/linediff.go`, `internal/git/invisible/invisible.go` and `internal/git/invisible/invisible_test.go`: the line edit script by longest common subsequence and the invisible-character checks.

## tuios tiling layouts

- Source: https://github.com/Gaurav-Gosain/tuios (`internal/layout/`, commit `aaaa128cd35b38ba8aa08a5f69e688f01385ec3e`)
- Copyright (c) 2025 Gaurav Gosain
- Used in `internal/core/tiling/`: `arrange.go`, `bsp.go`, `bsp_move.go`, `master.go`, `neighbour.go`, `scrolling.go`, `tiling.go` and their tests (`arrange_test.go`, `bsp_test.go`, `bsp_split_close_test.go`, `bsp_resize_axis_test.go`, `master_test.go`, `neighbour_test.go`, `tiling_test.go`, `export_test.go`).

## tuios diffview

- Source: https://github.com/Gaurav-Gosain/tuios (`internal/diffview/`, commit `aaaa128cd35b38ba8aa08a5f69e688f01385ec3e`)
- Copyright (c) 2025 Gaurav Gosain
- Used in `internal/ui/diffview/`: `diffview.go`, `highlight.go`, `pair.go`, `render.go`, `theme.go` and `lexers/` (`coded.go`, `gen.go`, `registry.go`): the diff line renderer, word-level change ranges and syntax highlighting. `shim.go` is MuxReveal's own.

## chroma lexer definitions

- Source: https://github.com/alecthomas/chroma (`lexers/embedded/*.xml` and the Go and Markdown lexers in `lexers/`) at v2.27.0, as carried by tuios `internal/diffview/lexers/`; the licence is in `internal/ui/diffview/lexers/COPYING`
- Copyright (c) 2017 Alec Thomas
- Used in `internal/ui/diffview/lexers/`: the gzipped `.xml.gz` definitions and `coded.go`.

## MIT License

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
