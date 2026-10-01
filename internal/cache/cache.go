// Package cache provides a small on-disk cache for HTTP GETs.
//
// The program page is ~5 MB and each speaker page ~3 MB, so generating promos
// for a handful of talks would otherwise re-download tens of megabytes on every
// run. Entries are keyed by URL hash and expire after a TTL.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultTimeout bounds a single fetch.
//
// Go's default client has none, so one unresponsive image host would hang a
// request — and with it a preview page — forever.
//
// Generous rather than tight: the program page is ~5 MB and a speaker page
// ~3.4 MB, and a slow conference network should not fail a fetch that would
// have succeeded.
const DefaultTimeout = 30 * time.Second

// Cache stores fetched response bodies under Dir.
type Cache struct {
	Dir string
	TTL time.Duration
	// Disabled bypasses both reads and writes, always hitting the network.
	Disabled bool
	// Timeout bounds one fetch, including connect, headers and body. Zero
	// means DefaultTimeout.
	Timeout time.Duration

	once   sync.Once
	client *http.Client
}

// httpClient returns the client for this cache, built once.
func (c *Cache) httpClient() *http.Client {
	c.once.Do(func() {
		timeout := c.Timeout
		if timeout <= 0 {
			timeout = DefaultTimeout
		}
		c.client = &http.Client{Timeout: timeout}
	})
	return c.client
}

// DefaultDir is the user cache directory this tool writes to.
func DefaultDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "cnd-promos")
	}
	return filepath.Join(base, "cnd-promos")
}

// New returns a cache in the default location with the given TTL.
func New(ttl time.Duration) *Cache {
	return &Cache{Dir: DefaultDir(), TTL: ttl}
}

func (c *Cache) path(url string) string {
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(c.Dir, hex.EncodeToString(sum[:16]))
}

// Get returns the body of url, from cache when a fresh entry exists.
func (c *Cache) Get(url string) ([]byte, error) {
	if !c.Disabled {
		if b, ok := c.read(url); ok {
			return b, nil
		}
	}

	resp, err := c.httpClient().Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}

	if !c.Disabled {
		// A cache write failure must not fail the command — the data is already
		// in hand, and the next run simply re-fetches.
		c.write(url, body)
	}
	return body, nil
}

func (c *Cache) read(url string) ([]byte, bool) {
	p := c.path(url)
	fi, err := os.Stat(p)
	if err != nil || time.Since(fi.ModTime()) > c.TTL {
		return nil, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	return b, true
}

func (c *Cache) write(url string, body []byte) {
	if os.MkdirAll(c.Dir, 0o755) != nil {
		return
	}
	p := c.path(url)
	// Write via a temporary file so a concurrent or interrupted run cannot
	// leave a truncated entry that later looks fresh.
	tmp, err := os.CreateTemp(c.Dir, "tmp-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return
	}
	if tmp.Close() != nil {
		return
	}
	os.Rename(tmp.Name(), p)
}

// FetchedAt is when url was last fetched into the cache, or the zero time when
// it is not cached (or caching is off).
//
// This is how a run knows how old its website data is without writing the time
// anywhere that would churn: the cache entry's own mtime is the fetch time.
func (c *Cache) FetchedAt(url string) time.Time {
	if c.Disabled {
		return time.Time{}
	}
	fi, err := os.Stat(c.path(url))
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}
