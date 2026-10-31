# Third-party material

MuxReveal includes material copied from the projects below, each under the MIT License (text at the end) unless its entry names another licence.

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
- Used in `internal/git/linediff/linediff.go`, `internal/git/invisible/invisible.go` and `internal/git/invisible/invisible_test.go`: the line edit script type, line splitting, the remove-all-then-add-all script and the invisible-character checks.

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

## fsnotify

- Source: https://github.com/fsnotify/fsnotify at v1.10.1: `fsnotify.go`, `shared.go`, `backend_inotify.go`, `backend_kqueue.go`, `system_bsd.go`, `system_darwin.go`, and from `internal/` the files `internal.go`, `darwin.go`, `freebsd.go`, `debug_darwin.go`, `debug_dragonfly.go`, `debug_freebsd.go`, `debug_kqueue.go`, `debug_linux.go`, `debug_netbsd.go`, `debug_openbsd.go`, `unix.go` and `unix2.go`
- Copyright (c) 2012 The Go Authors and the fsnotify Authors, under the BSD-3-Clause License; the text is in `internal/git/fsnotify/LICENSE`
- Used in `internal/git/fsnotify/`: the file watcher behind `git.Watch`. It differs from upstream in the import path of its `internal` package and in `backend_kqueue.go`, which carries the fix for https://github.com/fsnotify/fsnotify/issues/788 proposed in https://github.com/fsnotify/fsnotify/pull/789, adapted to v1.10.1: one mutex serializes every change to the watch set, `Close` leaves releasing the watches to the event reader and waits for it, and a failed registration closes only a descriptor opened in that call. It is a copy rather than a module requirement so that it can carry a fix upstream has not released yet; a `go.mod` replace would break `go install ...@latest`. The tests there, `close_test.go` and `kqueue_test.go`, are MuxReveal's own.

## Go WebP test image

- Source: https://cs.opensource.google/go/x/image (`testdata/gopher-doc.1bpp.lossless.webp`, v0.47.0)
- Copyright 2011 The Go Authors, under the BSD-3-Clause License (https://github.com/golang/image/blob/master/LICENSE)
- Used in `internal/ui/kitty/testdata/gopher.webp`: the WebP decode test.

## MIT License

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
