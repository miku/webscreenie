package capture

import "time"

// ImageType is the output image format.
type ImageType string

const (
	PNG  ImageType = "png"
	JPEG ImageType = "jpeg"
)

// Options controls how a screenshot is captured. The zero value is not
// directly useful; use DefaultOptions and override as needed.
type Options struct {
	// Input is a URL (http/https), a local file path, or a raw HTML string.
	Input string

	// Viewport dimensions in CSS pixels.
	Width  int
	Height int

	// ScaleFactor is the device scale factor (DPR). Values > 1 yield a
	// higher-fidelity, "retina" style capture.
	ScaleFactor float64

	// Type selects the output encoding (png or jpeg).
	Type ImageType

	// Quality is the JPEG quality in the range 0..100. Ignored for PNG.
	Quality int

	// FullPage captures the entire scrollable page instead of just the
	// viewport.
	FullPage bool

	// Element, when set, captures only the first DOM element matching this
	// CSS selector. Takes precedence over FullPage.
	Element string

	// WaitForElement blocks until a DOM element matching this CSS selector is
	// present (and visible) before capturing.
	WaitForElement string

	// Timeout is the maximum time to wait for the page to load. Zero disables
	// the timeout.
	Timeout time.Duration

	// Delay is an additional pause after load, before the capture is taken.
	Delay time.Duration

	// JavaScript toggles JavaScript execution in the page. Defaults to true.
	JavaScript bool

	// DarkMode emulates a "prefers-color-scheme: dark" preference.
	DarkMode bool

	// UserAgent overrides the browser user agent when non-empty.
	UserAgent string

	// Headers are extra HTTP headers sent with every request.
	Headers map[string]string

	// Insecure accepts self-signed and otherwise invalid TLS certificates.
	Insecure bool

	// Debug launches a visible (non-headless) browser window.
	Debug bool
}

// DefaultOptions returns a set of options aimed at a high-fidelity capture.
func DefaultOptions() Options {
	return Options{
		Width:       1280,
		Height:      800,
		ScaleFactor: 2,
		Type:        PNG,
		Quality:     100,
		JavaScript:  true,
		Headers:     map[string]string{},
		Timeout:     60 * time.Second,
	}
}
