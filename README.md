# webscreenie

Create a webpage screenshot from the command line.

```
$ webscreenie -o out.png http://example.com
```

By default the full viewport is captured at a 2× scale factor through a
headless Chrome browser, producing a high-fidelity PNG. This is a small,
idiomatic Go port of [sindresorhus/capture-website-cli][cw], using
[chromedp][chromedp] to drive Chrome over the DevTools Protocol and
[cobra][cobra] for the CLI.

[cw]: https://github.com/sindresorhus/capture-website-cli
[chromedp]: https://github.com/chromedp/chromedp
[cobra]: https://github.com/spf13/cobra

## Requirements

A Chrome or Chromium binary on your `PATH` (chromedp discovers it
automatically). No separate driver is needed.

## Install

```sh
go install github.com/miku/webscreenie@latest
```

Or build from a checkout:

```sh
go build -o webscreenie .
```

## Usage

The input may be a URL, a local HTML file, or HTML piped in via stdin (`-`).
If `--output` is omitted, the image is written to
`webscreenie-<timestamp>.<ext>` in the current directory.

```sh
# A URL
webscreenie -o shot.png https://example.com

# A local file
webscreenie -o page.png index.html

# Inline HTML from stdin
echo '<h1>Unicorn</h1>' | webscreenie -o hello.png -

# Full page, dark mode
webscreenie --full-page --dark-mode -o full.png https://example.com

# Just one element, as JPEG
webscreenie --element '.main' -t jpeg --quality 80 -o main.jpg https://example.com
```

## Options

| Flag                  | Default | Description                                         |
| --------------------- | ------- | --------------------------------------------------- |
| `-o, --output`        | auto    | Output file path                                    |
| `--width`             | 1280    | Viewport width                                      |
| `--height`            | 800     | Viewport height                                     |
| `--scale-factor`      | 2       | Device scale factor (DPR); >1 for retina fidelity   |
| `-t, --type`          | png     | Image type: `png` or `jpeg`                         |
| `--quality`           | 100     | JPEG quality 0..100 (ignored for PNG)               |
| `--full-page`         | false   | Capture the full scrollable page                    |
| `--element`           |         | Capture only the element matching this CSS selector |
| `--wait-for-element`  |         | Wait for this selector to be visible before capture |
| `--timeout`           | 60s     | Page-load timeout (`0` disables)                    |
| `--delay`             | 0       | Pause after load before capturing                   |
| `--javascript`        | true    | Enable JavaScript execution (`--javascript=false`)  |
| `--dark-mode`         | false   | Emulate a dark color-scheme preference              |
| `--user-agent`        |         | Override the browser user agent                     |
| `-H, --header`        |         | Extra HTTP header `Name: value` (repeatable)        |
| `--insecure`          | false   | Accept invalid TLS certificates                     |
| `--debug`             | false   | Show the browser window                             |
| `--overwrite`         | false   | Overwrite the output file if it exists              |

## Project layout

```
main.go                      entry point
cmd/root.go                  cobra command, flag parsing, I/O
internal/capture/options.go  Options struct and defaults
internal/capture/capture.go  chromedp capture logic
```

## Status

This is an early sketch covering the most-used options. The reference CLI has
several more (device emulation, ad blocking, cookies, PDF output, element
clipping/insets, script/style injection); these are intentionally left out for
now and are straightforward follow-ups against the `internal/capture` package.
