package core

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// The configured provider_presets_url must serve every fetch (#12). Setting
// the same URL again, as each config reload does, keeps the cached list.
func TestSetPresetsURLServesFetchesAndKeepsCacheForSameURL(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"version":1,"providers":[{"name":"custom","tier":1}]}`))
	}))
	defer srv.Close()

	c := globalPresetsCache
	c.mu.Lock()
	prevURL, prevData, prevAt := c.url, c.data, c.fetchedAt
	c.url, c.data = "", nil
	c.mu.Unlock()
	t.Cleanup(func() {
		c.mu.Lock()
		c.url, c.data, c.fetchedAt = prevURL, prevData, prevAt
		c.mu.Unlock()
	})

	SetPresetsURL(srv.URL)
	got, err := FetchProviderPresets()
	if err != nil {
		t.Fatalf("FetchProviderPresets: %v", err)
	}
	if len(got.Providers) != 1 || got.Providers[0].Name != "custom" {
		t.Fatalf("providers = %+v, want the list from the configured URL", got.Providers)
	}

	SetPresetsURL(srv.URL)
	if _, err := FetchProviderPresets(); err != nil {
		t.Fatalf("FetchProviderPresets: %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("server hit %d times, want 1: setting the same URL again must keep the cache", n)
	}

	SetPresetsURL(srv.URL + "/other")
	if _, err := FetchProviderPresets(); err != nil {
		t.Fatalf("FetchProviderPresets: %v", err)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("server hit %d times, want 2: a new URL must refetch", n)
	}
}
