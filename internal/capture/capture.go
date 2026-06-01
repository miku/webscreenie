// Package capture takes screenshots of web pages using a headless Chrome
// browser, driven over the Chrome DevTools Protocol via chromedp.
package capture

import (
	"context"
	"encoding/json"
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

	// Dismiss overlays (cookie banners etc.) before the capture.
	dismissed := false
	if len(opts.ClickSelectors) > 0 {
		tasks = append(tasks, clickElements(opts.ClickSelectors))
		dismissed = true
	}
	if len(opts.HideSelectors) > 0 {
		tasks = append(tasks, hideElements(opts.HideSelectors))
		dismissed = true
	}
	if opts.Aggressive {
		tasks = append(tasks, aggressiveDismiss())
		dismissed = true
	}
	if dismissed {
		// Give the page a moment to reflow after dismissals.
		tasks = append(tasks, chromedp.Sleep(200*time.Millisecond))
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

// hideElements injects a stylesheet that hides every matching selector. Each
// selector becomes its own rule so that a single invalid selector (EasyList
// occasionally contains them) cannot void the others.
func hideElements(selectors []string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var css strings.Builder
		for _, sel := range selectors {
			sel = strings.TrimSpace(sel)
			if sel == "" {
				continue
			}
			css.WriteString(sel)
			css.WriteString("{display:none !important;visibility:hidden !important}\n")
		}
		quoted, err := json.Marshal(css.String())
		if err != nil {
			return err
		}
		expr := fmt.Sprintf(`(function(){try{var s=document.createElement('style');`+
			`s.textContent=%s;document.documentElement.appendChild(s);}catch(e){}})()`, quoted)
		return chromedp.Evaluate(expr, nil).Do(ctx)
	})
}

// clickElements clicks the first element matching each selector, ignoring
// selectors that match nothing or are syntactically invalid.
func clickElements(selectors []string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		for _, sel := range selectors {
			sel = strings.TrimSpace(sel)
			if sel == "" {
				continue
			}
			quoted, err := json.Marshal(sel)
			if err != nil {
				return err
			}
			expr := fmt.Sprintf(`(function(){try{var el=document.querySelector(%s);`+
				`if(el){el.click();return true;}}catch(e){}return false;})()`, quoted)
			if err := chromedp.Evaluate(expr, nil).Do(ctx); err != nil {
				return err
			}
		}
		return nil
	})
}

// aggressiveScript is a best-effort, heuristic banner remover. It hides
// fixed/sticky overlays that look like cookie/consent banners (by id, class,
// ARIA role or visible text), removes full-screen translucent backdrops, and
// lifts any scroll-lock the banner imposed on the document. It is deliberately
// conservative about what counts as a banner (overlay positioning plus a
// cookie/consent signal) to limit false positives, but can still occasionally
// hide a legitimate fixed element.
const aggressiveScript = `(() => {
  try {
    const kw = /(cookie|consent|gdpr|ccpa|\bprivacy\b|datenschutz|einwillig|zustimm|tracking|interest[- ]based|we use|uses cookies|akzeptier)/i;
    const vw = window.innerWidth, vh = window.innerHeight;
    const hide = el => el.style.setProperty('display', 'none', 'important');
    const cls = el => (typeof el.className === 'string' ? el.className : (el.getAttribute && el.getAttribute('class')) || '');

    const nodes = document.body ? document.body.querySelectorAll('*') : [];
    for (const el of nodes) {
      const cs = getComputedStyle(el);
      if (cs.position !== 'fixed' && cs.position !== 'sticky') continue;
      if (cs.display === 'none' || cs.visibility === 'hidden') continue;
      const r = el.getBoundingClientRect();
      if (r.width < 1 || r.height < 1) continue;

      const coversWidth = r.width >= vw * 0.6;
      const fullScreen = r.width >= vw * 0.9 && r.height >= vh * 0.9;
      const text = (el.innerText || '').slice(0, 600);
      const role = (el.getAttribute && el.getAttribute('role')) || '';
      const looksCookie = kw.test(text) || kw.test(el.id) || kw.test(cls(el));

      // An edge bar or modal that mentions cookies/consent.
      if (looksCookie && (coversWidth || fullScreen || role === 'dialog')) { hide(el); continue; }
      // A full-screen backdrop with a background and little text.
      if (fullScreen && text.trim().length < 40) {
        const bg = cs.backgroundColor;
        if (bg && bg !== 'transparent' && bg !== 'rgba(0, 0, 0, 0)') { hide(el); continue; }
      }
    }

    // Restore scrolling a banner may have locked.
    for (const el of [document.documentElement, document.body]) {
      if (!el) continue;
      const cs = getComputedStyle(el);
      if (cs.overflow === 'hidden' || cs.overflowY === 'hidden') {
        el.style.setProperty('overflow', 'auto', 'important');
      }
      if (cs.position === 'fixed') el.style.setProperty('position', 'static', 'important');
    }
  } catch (e) {}
})()`

// aggressiveDismiss runs aggressiveScript in the page.
func aggressiveDismiss() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return chromedp.Evaluate(aggressiveScript, nil).Do(ctx)
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
