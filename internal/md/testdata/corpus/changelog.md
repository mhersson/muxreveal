# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `--drafts` flag to include draft pages in a build
- Support for nested `content/` folders, for example `content/blog/2026/`

## [1.2.0] - 2026-09-30

### Added

- A `watch` command that rebuilds on change
- Syntax highlighting for fenced code in `go`, `sh` and `toml`

### Changed

- Pages are now written in parallel; a build of 1,000 pages went from 2.1 s to 0.8 s
- The default output folder is `dist` instead of `public`

### Fixed

- Relative links in nested pages no longer point one folder too high ([#41](https://github.com/example/gleam/issues/41))
- A crash on an empty front matter block
  - it also hit files with only a comment
  - both now build an empty page

## [1.1.0] - 2026-07-02

### Added

- Table of contents for pages with more than three headings

### Deprecated

- The `--public` flag, use `--out` instead

### Removed

- The unmaintained `legacy` theme

## [1.0.0] - 2026-05-15

- Initial release

[Unreleased]: https://github.com/example/gleam/compare/v1.2.0...HEAD
[1.2.0]: https://github.com/example/gleam/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/example/gleam/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/example/gleam/releases/tag/v1.0.0
