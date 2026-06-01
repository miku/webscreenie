// Package filterlist downloads, caches and parses EasyList-style cosmetic
// element-hiding rules, which are used to hide cookie-consent banners (and
// similar overlays) before a screenshot is taken.
//
// Only plain element-hide rules ("domains##selector") are understood. Network
// filters, exceptions ("#@#"), procedural/extended selectors and scriptlet
// injections ("#?#", "#$#", "+js(...)") are ignored, since we only need CSS
// selectors to hide with `display:none`.
package filterlist

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// DefaultURL is Fanboy's Cookie List ("cookiemonster"): element-hiding rules
// dedicated to cookie-consent banners. (Note: this is distinct from the
// general-purpose easylist.txt, which contains very few banner rules.)
const DefaultURL = "https://secure.fanboy.co.nz/fanboy-cookiemonster.txt"

// Options controls loading of a filter list.
type Options struct {
	// URL is the source to download from. Defaults to DefaultURL when empty.
	URL string
	// Update forces a re-download even if a cached copy exists.
	Update bool
	// HTTPClient is used for downloads. Defaults to http.DefaultClient.
	HTTPClient *http.Client
}

// FilterList holds parsed cosmetic rules, split into generic rules (applied to
// every page) and per-domain rules.
type FilterList struct {
	generic []string
	domain  map[string][]string
}

// Load returns a parsed filter list, downloading and caching it under the
// user cache directory (XDG_CACHE_HOME on Linux) when needed. If a download
// fails but a cached copy exists, the cached copy is used.
func Load(ctx context.Context, opts Options) (*FilterList, error) {
	if opts.URL == "" {
		opts.URL = DefaultURL
	}
	cachePath, err := CachePath(opts.URL)
	if err != nil {
		return nil, err
	}

	_, statErr := os.Stat(cachePath)
	if opts.Update || statErr != nil {
		if err := download(ctx, opts, cachePath); err != nil {
			if statErr != nil {
				// No cached copy to fall back to.
				return nil, err
			}
			// Keep using the stale cache, but surface the failure upstream so
			// the caller can warn.
			return nil, fmt.Errorf("refresh failed (using cached copy): %w", errWithCache{err, cachePath})
		}
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, fmt.Errorf("reading filter list cache: %w", err)
	}
	return parse(data), nil
}

// errWithCache lets Load signal "download failed, but a usable cache exists".
type errWithCache struct {
	err  error
	path string
}

func (e errWithCache) Error() string { return e.err.Error() }

// CachePath returns the on-disk location for the list downloaded from rawURL.
func CachePath(rawURL string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating cache dir: %w", err)
	}
	name := path.Base(rawURL)
	if name == "" || name == "." || name == "/" {
		name = "filterlist.txt"
	}
	return filepath.Join(base, "webscreenie", name), nil
}

func download(ctx context.Context, opts Options, dest string) error {
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.URL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", opts.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: unexpected status %s", opts.URL, resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	// Write to a temp file and rename for an atomic replace.
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".filterlist-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}

// proceduralMarkers are substrings that indicate a non-CSS (procedural or
// scriptlet) selector we cannot apply as plain `display:none`.
var proceduralMarkers = []string{
	"+js(", ":-abp-", ":has-text(", ":contains(", ":matches-css",
	":min-text-length", ":upward(", ":nth-ancestor(", ":watch-attr",
	":remove(", ":style(", ":xpath(", ":matches-path",
}

func parse(data []byte) *FilterList {
	fl := &FilterList{domain: map[string][]string{}}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "[") {
			continue // blank, comment or metadata
		}
		// Element-hide rules use the "##" separator. Exceptions ("#@#") and
		// procedural/snippet rules ("#?#", "#$#") use other separators and so
		// do not contain "##".
		idx := strings.Index(line, "##")
		if idx < 0 {
			continue
		}
		domains, selector := line[:idx], line[idx+2:]
		if selector == "" || containsProcedural(selector) {
			continue
		}
		if domains == "" {
			fl.generic = append(fl.generic, selector)
			continue
		}
		for _, d := range strings.Split(domains, ",") {
			d = strings.TrimSpace(strings.ToLower(d))
			if d == "" || strings.HasPrefix(d, "~") {
				continue // skip excluded-domain rules for simplicity
			}
			fl.domain[d] = append(fl.domain[d], selector)
		}
	}
	return fl
}

func containsProcedural(selector string) bool {
	for _, m := range proceduralMarkers {
		if strings.Contains(selector, m) {
			return true
		}
	}
	return false
}

// SelectorsFor returns the selectors applicable to host: all generic rules
// plus any rule whose domain matches host or one of its parent domains. When
// host is empty (e.g. local files or inline HTML), only generic rules apply.
func (fl *FilterList) SelectorsFor(host string) []string {
	out := make([]string, len(fl.generic))
	copy(out, fl.generic)
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return out
	}
	for d, sels := range fl.domain {
		if host == d || strings.HasSuffix(host, "."+d) {
			out = append(out, sels...)
		}
	}
	return out
}

// Len reports the number of generic and domain-specific rules parsed.
func (fl *FilterList) Len() (generic, domain int) {
	n := 0
	for _, sels := range fl.domain {
		n += len(sels)
	}
	return len(fl.generic), n
}

// CacheAge returns how old the cached copy for rawURL is, or false if there is
// no cached copy.
func CacheAge(rawURL string) (time.Duration, bool) {
	p, err := CachePath(rawURL)
	if err != nil {
		return 0, false
	}
	info, err := os.Stat(p)
	if err != nil {
		return 0, false
	}
	return time.Since(info.ModTime()), true
}
