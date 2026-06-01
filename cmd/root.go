// Package cmd wires the webscreenie command-line interface to the capture
// package.
package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/miku/webscreenie/internal/capture"
	"github.com/spf13/cobra"
)

// flags holds the raw CLI flag values before they are translated into
// capture.Options.
type flags struct {
	output      string
	width       int
	height      int
	scaleFactor float64
	imageType   string
	quality     int
	fullPage    bool
	element     string
	waitElement string
	timeout     time.Duration
	delay       time.Duration
	javascript  bool
	darkMode    bool
	userAgent   string
	headers     []string
	insecure    bool
	debug       bool
	overwrite   bool
}

var f flags

var rootCmd = &cobra.Command{
	Use:   "webscreenie [flags] <url|file|->",
	Short: "Capture a screenshot of a webpage from the command line",
	Long: `webscreenie captures a screenshot of a webpage using a headless Chrome browser.

The input may be a URL, a local HTML file, or HTML read from stdin ("-").
By default a high-fidelity, full-viewport PNG is written to
webscreenie-YYYYMMDDHHMMSS.png in the current directory.`,
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE:         run,
}

// Execute runs the root command and exits non-zero on error.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "webscreenie:", err)
		os.Exit(1)
	}
}

func init() {
	fl := rootCmd.Flags()
	fl.StringVarP(&f.output, "output", "o", "", "output file path (default webscreenie-<timestamp>.<ext>)")
	fl.IntVar(&f.width, "width", 1280, "viewport width")
	fl.IntVar(&f.height, "height", 800, "viewport height")
	fl.Float64Var(&f.scaleFactor, "scale-factor", 2, "device scale factor (DPR)")
	fl.StringVarP(&f.imageType, "type", "t", "png", "image type: png or jpeg")
	fl.IntVar(&f.quality, "quality", 100, "image quality 0..100 (jpeg only)")
	fl.BoolVar(&f.fullPage, "full-page", false, "capture the full scrollable page")
	fl.StringVar(&f.element, "element", "", "capture only the element matching this CSS selector")
	fl.StringVar(&f.waitElement, "wait-for-element", "", "wait for the element matching this CSS selector before capturing")
	fl.DurationVar(&f.timeout, "timeout", 60*time.Second, "page load timeout (0 to disable)")
	fl.DurationVar(&f.delay, "delay", 0, "wait this long after load before capturing")
	fl.BoolVar(&f.javascript, "javascript", true, "enable JavaScript execution")
	fl.BoolVar(&f.darkMode, "dark-mode", false, "emulate a dark color-scheme preference")
	fl.StringVar(&f.userAgent, "user-agent", "", "override the browser user agent")
	fl.StringArrayVarP(&f.headers, "header", "H", nil, "extra HTTP header 'Name: value' (repeatable)")
	fl.BoolVar(&f.insecure, "insecure", false, "accept invalid TLS certificates")
	fl.BoolVar(&f.debug, "debug", false, "show the browser window")
	fl.BoolVar(&f.overwrite, "overwrite", false, "overwrite the output file if it exists")
}

func run(cmd *cobra.Command, args []string) error {
	opts := capture.DefaultOptions()
	opts.Width = f.width
	opts.Height = f.height
	opts.ScaleFactor = f.scaleFactor
	opts.Quality = f.quality
	opts.FullPage = f.fullPage
	opts.Element = f.element
	opts.WaitForElement = f.waitElement
	opts.Timeout = f.timeout
	opts.Delay = f.delay
	opts.JavaScript = f.javascript
	opts.DarkMode = f.darkMode
	opts.UserAgent = f.userAgent
	opts.Insecure = f.insecure
	opts.Debug = f.debug

	switch strings.ToLower(f.imageType) {
	case "png":
		opts.Type = capture.PNG
	case "jpeg", "jpg":
		opts.Type = capture.JPEG
	default:
		return fmt.Errorf("unsupported image type %q (use png or jpeg)", f.imageType)
	}

	headers, err := parseHeaders(f.headers)
	if err != nil {
		return err
	}
	opts.Headers = headers

	input, err := resolveInputArg(cmd, args)
	if err != nil {
		return err
	}
	opts.Input = input

	output := f.output
	if output == "" {
		output = defaultOutputName(opts.Type)
	}
	if !f.overwrite {
		if _, err := os.Stat(output); err == nil {
			return fmt.Errorf("output file %q already exists (use --overwrite)", output)
		}
	}

	buf, err := capture.Capture(cmd.Context(), opts)
	if err != nil {
		return err
	}

	if err := os.WriteFile(output, buf, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", output, err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", output, len(buf))
	return nil
}

// resolveInputArg returns the positional argument, or reads HTML from stdin
// when the argument is "-" or absent and stdin is piped.
func resolveInputArg(cmd *cobra.Command, args []string) (string, error) {
	if len(args) == 1 && args[0] != "-" {
		return args[0], nil
	}
	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", fmt.Errorf("reading stdin: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return "", fmt.Errorf("no input: specify a URL, file path, or pipe HTML via stdin")
	}
	return string(data), nil
}

func parseHeaders(raw []string) (map[string]string, error) {
	headers := make(map[string]string, len(raw))
	for _, h := range raw {
		name, value, ok := strings.Cut(h, ":")
		if !ok {
			return nil, fmt.Errorf("invalid header %q (expected 'Name: value')", h)
		}
		headers[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	return headers, nil
}

func defaultOutputName(t capture.ImageType) string {
	ext := "png"
	if t == capture.JPEG {
		ext = "jpg"
	}
	return fmt.Sprintf("webscreenie-%s.%s", time.Now().Format("20060102150405"), ext)
}
