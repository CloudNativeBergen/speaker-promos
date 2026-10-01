package cache

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestGetAndCache(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte("payload"))
	}))
	defer srv.Close()

	c := &Cache{Dir: t.TempDir(), TTL: time.Minute}
	for i := range 3 {
		body, err := c.Get(srv.URL)
		if err != nil {
			t.Fatalf("Get %d: %v", i, err)
		}
		if string(body) != "payload" {
			t.Errorf("body = %q", body)
		}
	}
	if hits != 1 {
		t.Errorf("origin was hit %d times, want 1", hits)
	}

	// Disabled bypasses the cache in both directions.
	c.Disabled = true
	if _, err := c.Get(srv.URL); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Errorf("origin hits = %d, want the disabled cache to refetch", hits)
	}
}

func TestGetRejectsNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := &Cache{Dir: t.TempDir(), TTL: time.Minute}
	if _, err := c.Get(srv.URL); err == nil {
		t.Fatal("want an error for a 404")
	}
	// A failure must not be cached as if it were content.
	if entries, _ := filepath.Glob(filepath.Join(c.Dir, "*")); len(entries) != 0 {
		t.Errorf("a failed fetch left %d cache entries", len(entries))
	}
}

// Regression: there was no timeout, and Go's default client has none either, so
// one host that accepted the connection and never answered blocked the fetch
// forever. In the preview server that fetch held the lock every page render
// needs, so a single dead photo URL wedged the whole app.
func TestGetTimesOutOnAnUnresponsiveHost(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // never answers until the test lets go
	}))
	defer srv.Close()
	defer close(block)

	c := &Cache{Dir: t.TempDir(), TTL: time.Minute, Timeout: 200 * time.Millisecond}

	done := make(chan error, 1)
	start := time.Now()
	go func() { _, err := c.Get(srv.URL); done <- err }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want an error from a host that never answers")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("took %v to give up", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Get never returned: the timeout is not being applied")
	}
}

func TestDefaultTimeoutIsApplied(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	if got := c.httpClient().Timeout; got != DefaultTimeout {
		t.Errorf("client timeout = %v, want %v", got, DefaultTimeout)
	}
	// An explicit timeout wins, and the client is built once.
	c2 := &Cache{Dir: t.TempDir(), Timeout: time.Second}
	if got := c2.httpClient().Timeout; got != time.Second {
		t.Errorf("client timeout = %v", got)
	}
	if c2.httpClient() != c2.httpClient() {
		t.Error("a new client is built per call")
	}
}
