// Package fetch is glean's HTTP front door: one client that identifies itself,
// obeys robots.txt, waits its turn per host, and remembers what it saw last time
// so that a daily re-scan of forty pages costs forty conditional requests and
// almost no bytes.
package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultUserAgent identifies the crawler and points a site owner at something
// they can read. An unattributed scraper is one an administrator can only block.
const DefaultUserAgent = "gleanbot/0.1 (+https://github.com/navingamage/glean; personal class action tracker)"

// BrowserUserAgent is the one-shot retry for a site that answers 403 to an
// identifying agent.
//
// This is a deliberate and slightly uncomfortable choice, so it is worth writing
// down. robots.txt is the machine-readable statement of what a site owner wants
// crawled, and it is honoured here as a hard gate that this retry does not
// touch. A 403 on a public marketing page that robots.txt explicitly permits is
// not that statement; it is a firewall rule matching on the shape of the
// User-Agent string. Retrying once, at one request per second, for one person's
// own settlement notices, is a different thing from evading a stated policy. If
// a site disallows the path, glean does not fetch it at all.
const BrowserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36"

// maxBody caps a single response. Notice pages are a few tens of kilobytes; a
// multi-megabyte response is a PDF, a video or a mistake.
const maxBody = 4 << 20

// ErrDisallowed is returned when robots.txt forbids a URL. It is not a failure
// of the scan and callers treat it as a skip.
var ErrDisallowed = errors.New("fetch: disallowed by robots.txt")

type Client struct {
	HTTP      *http.Client
	UserAgent string
	// FallbackUserAgent is retried once when a request is refused outright. Set
	// it to the empty string to never retry.
	FallbackUserAgent string
	CacheDir          string
	MinInterval       time.Duration // floor on the gap between requests to one host
	IgnoreCache       bool

	mu      sync.Mutex
	lastHit map[string]time.Time
	robots  map[string]*robots
}

func New(cacheDir string) *Client {
	return &Client{
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("too many redirects")
				}
				return nil
			},
		},
		UserAgent:         DefaultUserAgent,
		FallbackUserAgent: BrowserUserAgent,
		CacheDir:          cacheDir,
		MinInterval:       time.Second,
		lastHit:           map[string]time.Time{},
		robots:            map[string]*robots{},
	}
}

// Response is a fetched page plus what glean needs to know about whether it has
// changed since last time.
type Response struct {
	URL         string    `json:"url"`
	Status      int       `json:"status"`
	Body        string    `json:"-"`
	ContentType string    `json:"content_type"`
	Hash        string    `json:"hash"`
	ETag        string    `json:"etag,omitempty"`
	Modified    string    `json:"modified,omitempty"`
	FetchedAt   time.Time `json:"fetched_at"`

	// Changed is false when the server said 304, or when the body came back
	// byte-identical to the cached copy. A settlement page that has not changed
	// does not need to be re-extracted, and re-extraction is the expensive step.
	Changed bool `json:"-"`
}

// Get fetches a URL, consulting robots.txt and the on-disk cache first.
func (c *Client) Get(ctx context.Context, rawURL string) (*Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("fetch: unsupported scheme %q", u.Scheme)
	}

	rules, err := c.robotsFor(ctx, u)
	if err == nil && !rules.allowed(u.EscapedPath()) {
		return nil, ErrDisallowed
	}

	cached := c.load(rawURL)
	c.wait(u.Host, rules)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.1")
	req.Header.Set("Accept-Language", "en-CA,en;q=0.9,fr-CA;q=0.5")
	if cached != nil && !c.IgnoreCache {
		if cached.ETag != "" {
			req.Header.Set("If-None-Match", cached.ETag)
		}
		if cached.Modified != "" {
			req.Header.Set("If-Modified-Since", cached.Modified)
		}
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotAcceptable) &&
		c.FallbackUserAgent != "" && req.Header.Get("User-Agent") != c.FallbackUserAgent {
		resp.Body.Close()
		c.wait(u.Host, rules)
		req.Header.Set("User-Agent", c.FallbackUserAgent)
		resp, err = c.HTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", rawURL, err)
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified && cached != nil {
		cached.Changed = false
		cached.FetchedAt = time.Now()
		c.save(cached)
		return cached, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: %s", rawURL, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	sum := sha256.Sum256(body)

	out := &Response{
		URL:         rawURL,
		Status:      resp.StatusCode,
		Body:        string(body),
		ContentType: resp.Header.Get("Content-Type"),
		Hash:        hex.EncodeToString(sum[:]),
		ETag:        resp.Header.Get("ETag"),
		Modified:    resp.Header.Get("Last-Modified"),
		FetchedAt:   time.Now(),
	}
	out.Changed = cached == nil || cached.Hash != out.Hash
	c.save(out)
	return out, nil
}

// wait enforces the per-host gap, taking whichever is longer of the configured
// floor and the site's own Crawl-delay.
func (c *Client) wait(host string, rules *robots) {
	gap := c.MinInterval
	if rules != nil && rules.delay > gap {
		gap = rules.delay
	}
	c.mu.Lock()
	last := c.lastHit[host]
	now := time.Now()
	next := last.Add(gap)
	if next.After(now) {
		c.lastHit[host] = next
		c.mu.Unlock()
		time.Sleep(next.Sub(now))
		return
	}
	c.lastHit[host] = now
	c.mu.Unlock()
}

func (c *Client) robotsFor(ctx context.Context, u *url.URL) (*robots, error) {
	key := u.Scheme + "://" + u.Host
	c.mu.Lock()
	if r, ok := c.robots[key]; ok {
		c.mu.Unlock()
		return r, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, key+"/robots.txt", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)

	// A missing or unreadable robots.txt means no restrictions, which is the
	// standard's reading and the only one that does not turn a flaky server into
	// a permanent refusal to crawl.
	r := &robots{}
	if resp, err := c.HTTP.Do(req); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			if b, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10)); err == nil {
				r = parseRobots(string(b), c.UserAgent)
			}
		}
	}
	c.mu.Lock()
	c.robots[key] = r
	c.mu.Unlock()
	return r, nil
}

func (c *Client) cachePath(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return filepath.Join(c.CacheDir, hex.EncodeToString(sum[:])+".json")
}

func (c *Client) load(rawURL string) *Response {
	if c.CacheDir == "" || c.IgnoreCache {
		return nil
	}
	b, err := os.ReadFile(c.cachePath(rawURL))
	if err != nil {
		return nil
	}
	var wire struct {
		Response
		Body string `json:"body"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return nil
	}
	out := wire.Response
	out.Body = wire.Body
	return &out
}

func (c *Client) save(r *Response) {
	if c.CacheDir == "" {
		return
	}
	if err := os.MkdirAll(c.CacheDir, 0o700); err != nil {
		return
	}
	wire := struct {
		Response
		Body string `json:"body"`
	}{Response: *r, Body: r.Body}
	b, err := json.Marshal(wire)
	if err != nil {
		return
	}
	_ = os.WriteFile(c.cachePath(r.URL), b, 0o600)
}

// IsHTML reports whether a response is worth handing to the HTML extractor.
func (r *Response) IsHTML() bool {
	ct := strings.ToLower(r.ContentType)
	return ct == "" || strings.Contains(ct, "html") || strings.Contains(ct, "xml") ||
		strings.Contains(ct, "text/plain")
}
