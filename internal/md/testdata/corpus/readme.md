<p align="center">
  <img src="./docs/shot.png" alt="Gleam logo" width="200">
  <br>
  <b>Gleam</b> - a tiny, fast static site builder
</p>

<p align="center">
  <a href="https://example.com/gleam">Website</a> |
  <a href="https://example.com/gleam/docs">Docs</a>
</p>

[![Build](https://img.shields.io/github/actions/workflow/status/example/gleam/ci.yml)](https://github.com/example/gleam/actions)
[![Release](https://img.shields.io/github/v/release/example/gleam)][release]
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

# Gleam

Gleam turns a folder of Markdown into a site. It has **no runtime**, reads a single [config file][config], and builds a thousand pages in under a second. See the [changelog][changes] and the [contributing guide](./CONTRIBUTING.md).

![A screenshot of the generated site](./docs/shot.png)

## Install

```sh
go install example.com/gleam/cmd/gleam@latest
```

Or download a binary from the [releases page][release]. Contact <mailto:hello@example.com> or https://example.com/gleam for help.

## Usage

1. Create `gleam.toml`
2. Put pages in `content/`
3. Run `gleam build`

| Flag        | Default | Meaning                       |
| ----------- | ------- | ----------------------------- |
| `--out`     | `dist`  | where the site is written     |
| `--drafts`  | off     | include pages marked as draft |

## License

MIT, see [LICENSE](LICENSE).

[config]: https://example.com/gleam/docs/config "Configuration"
[release]: https://github.com/example/gleam/releases
[changes]: ./CHANGELOG.md
