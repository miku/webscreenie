// Package capture takes screenshots of web pages using a headless Chrome
// browser, driven over the Chrome DevTools Protocol via chromedp.
package capture

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Capture renders the page described by opts and returns the encoded image
// bytes.
func Capture(ctx context.Context, opts Options) ([]byte, error) {
	if strings.TrimSpace(opts.Input) == "" {
		return nil, fmt.Errorf("no input: specify a URL, file path or HTML")
	}

	navURL, err := resolveInput(opts.Input)
	if err != nil {
		return nil, err
	}

	allocOpts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	allocOpts = append(allocOpts,
		chromedp.WindowSize(opts.Width, opts.Height),
	)
	if opts.UserAgent != "" {
		allocOpts = append(allocOpts, chromedp.UserAgent(opts.UserAgent))
	}
	if opts.Insecure {
		allocOpts = append(allocOpts, chromedp.Flag("ignore-certificate-errors", true))
	}
	if opts.Debug {
		allocOpts = append(allocOpts, chromedp.Flag("headless", false))
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	if opts.Timeout > 0 {
		var cancelTimeout context.CancelFunc
		browserCtx, cancelTimeout = context.WithTimeout(browserCtx, opts.Timeout)
		defer cancelTimeout()
	}

	var buf []byte
	if err := chromedp.Run(browserCtx, buildTasks(navURL, opts, &buf)...); err != nil {
		return nil, fmt.Errorf("capture failed: %w", err)
	}
	return buf, nil
}

// buildTasks assembles the ordered chromedp actions for a capture.
func buildTasks(navURL string, opts Options, buf *[]byte) chromedp.Tasks {
	var tasks chromedp.Tasks

	// Pre-navigation setup.
	tasks = append(tasks, chromedp.ActionFunc(func(ctx context.Context) error {
		if err := emulation.SetDeviceMetricsOverride(
			int64(opts.Width), int64(opts.Height), opts.ScaleFactor, false).Do(ctx); err != nil {
			return err
		}
		if !opts.JavaScript {
			if err := emulation.SetScriptExecutionDisabled(true).Do(ctx); err != nil {
				return err
			}
		}
		if opts.DarkMode {
			if err := emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{
				{Name: "prefers-color-scheme", Value: "dark"},
			}).Do(ctx); err != nil {
				return err
			}
		}
		if len(opts.Headers) > 0 {
			if err := network.Enable().Do(ctx); err != nil {
				return err
			}
			headers := network.Headers{}
			for k, v := range opts.Headers {
				headers[k] = v
			}
			if err := network.SetExtraHTTPHeaders(headers).Do(ctx); err != nil {
				return err
			}
		}
		return nil
	}))

	tasks = append(tasks, chromedp.Navigate(navURL))

	if opts.WaitForElement != "" {
		tasks = append(tasks, chromedp.WaitVisible(opts.WaitForElement, chromedp.ByQuery))
	}
	if opts.Delay > 0 {
		tasks = append(tasks, chromedp.Sleep(opts.Delay))
	}

	switch {
	case opts.Element != "":
		tasks = append(tasks, chromedp.WaitVisible(opts.Element, chromedp.ByQuery))
		tasks = append(tasks, chromedp.Screenshot(opts.Element, buf, chromedp.ByQuery))
	case opts.FullPage:
		tasks = append(tasks, expandScrollContainers())
		tasks = append(tasks, fullPageScreenshot(opts, buf))
	default:
		tasks = append(tasks, viewportScreenshot(opts, buf))
	}

	return tasks
}

// viewportScreenshot captures just the current viewport.
func viewportScreenshot(opts Options, buf *[]byte) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		*buf, err = page.CaptureScreenshot().
			WithFormat(screenshotFormat(opts.Type)).
			WithQuality(qualityFor(opts)).
			Do(ctx)
		return err
	})
}

// expandScript neutralizes viewport-pinned heights and inner overflow so that
// content scrolled inside an app shell (common in single-page apps) reflows
// into the normal document flow, where a full-page capture can reach it.
const expandScript = `(() => {
  for (const el of [document.documentElement, document.body]) {
    if (!el) continue;
    el.style.height = 'auto';
    el.style.maxHeight = 'none';
    el.style.overflow = 'visible';
  }
  for (const el of document.querySelectorAll('*')) {
    const oy = getComputedStyle(el).overflowY;
    if ((oy === 'auto' || oy === 'scroll' || oy === 'hidden') &&
        el.scrollHeight > el.clientHeight + 1) {
      el.style.height = 'auto';
      el.style.maxHeight = 'none';
      el.style.overflow = 'visible';
    }
  }
})()`

// expandScrollContainers runs expandScript and gives the page a brief moment
// to reflow before the page is measured for a full-page capture.
func expandScrollContainers() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if err := chromedp.Evaluate(expandScript, nil).Do(ctx); err != nil {
			return fmt.Errorf("expanding scroll containers: %w", err)
		}
		return chromedp.Sleep(100 * time.Millisecond).Do(ctx)
	})
}

// fullPageScreenshot captures the entire scrollable page. It first measures
// the real content size (which can exceed the viewport, and on many SPAs is
// pinned to the viewport height until measured) and resizes the emulated
// viewport to match before capturing.
func fullPageScreenshot(opts Options, buf *[]byte) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		_, _, _, _, _, contentSize, err := page.GetLayoutMetrics().Do(ctx)
		if err != nil {
			return fmt.Errorf("measuring layout: %w", err)
		}
		if contentSize == nil || contentSize.Width == 0 || contentSize.Height == 0 {
			return fmt.Errorf("could not determine page content size")
		}

		width := int64(math.Ceil(contentSize.Width))
		height := int64(math.Ceil(contentSize.Height))

		// Resize the emulated viewport to the full content so layout reflows
		// to its natural height before we capture.
		if err := emulation.SetDeviceMetricsOverride(width, height, opts.ScaleFactor, false).Do(ctx); err != nil {
			return fmt.Errorf("resizing viewport: %w", err)
		}

		*buf, err = page.CaptureScreenshot().
			WithFormat(screenshotFormat(opts.Type)).
			WithQuality(qualityFor(opts)).
			WithCaptureBeyondViewport(true).
			WithFromSurface(true).
			WithClip(&page.Viewport{
				X:      contentSize.X,
				Y:      contentSize.Y,
				Width:  contentSize.Width,
				Height: contentSize.Height,
				Scale:  1,
			}).
			Do(ctx)
		return err
	})
}

func screenshotFormat(t ImageType) page.CaptureScreenshotFormat {
	if t == JPEG {
		return page.CaptureScreenshotFormatJpeg
	}
	return page.CaptureScreenshotFormatPng
}

func qualityFor(opts Options) int64 {
	if opts.Type == JPEG {
		return int64(opts.Quality)
	}
	return 0
}

// resolveInput turns user input into a navigable URL. It accepts http(s)
// URLs, existing local files (served via file://), and raw HTML (served via a
// data: URL).
func resolveInput(input string) (string, error) {
	if u, err := url.Parse(input); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return input, nil
	}
	if info, err := os.Stat(input); err == nil && !info.IsDir() {
		abs, err := filepath.Abs(input)
		if err != nil {
			return "", err
		}
		return "file://" + abs, nil
	}
	if looksLikeHTML(input) {
		return "data:text/html;charset=utf-8," + url.PathEscape(input), nil
	}
	return "", fmt.Errorf("input is neither a URL, an existing file, nor HTML: %q", input)
}

func looksLikeHTML(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "<")
}
