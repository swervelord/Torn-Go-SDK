package torn

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// RequestEvent is the redacted payload passed to hooks (PARITY_SPEC P8.3). URL
// never contains a key; KeyHint is "***" plus at most four key characters.
type RequestEvent struct {
	Operation string
	Method    string
	URL       string
	Version   string
	// Attempt counts sends of this request, starting at 1.
	Attempt int
	Route   string
	KeyHint string
	// Status is the HTTP status (0 before a response or on transport errors).
	Status int
	// Code is the Torn error code, or 0.
	Code     int
	Duration time.Duration
	// Delay is the wait before the next attempt (OnRetry only).
	Delay time.Duration
	// Err is the failure for OnRetry and OnError (redacted).
	Err error
}

// Hooks observe the executor. Hooks run synchronously on the calling
// goroutine and must not block for long; a panicking hook is recovered.
type Hooks struct {
	// OnRequest runs before every send.
	OnRequest func(RequestEvent)
	// OnResponse runs after every successful response.
	OnResponse func(RequestEvent)
	// OnRetry runs before an automatic retry (key failover, backoff or rate wait).
	OnRetry func(RequestEvent)
	// OnError runs when a request fails for good.
	OnError func(RequestEvent)
}

func (c *Client) emit(hook func(RequestEvent), e RequestEvent) {
	if hook == nil {
		return
	}
	defer func() { _ = recover() }()
	hook(e)
}

// Cache stores successful raw responses (P8.4). Implementations must be safe
// for concurrent use. Errors are never cached.
type Cache interface {
	Get(key string) ([]byte, bool)
	Set(key string, value []byte, ttl time.Duration)
}

// MemoryCache is an in-memory TTL Cache. The zero value is not usable; call
// NewMemoryCache.
type MemoryCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	now     func() time.Time
	max     int
}

type cacheEntry struct {
	value   []byte
	expires time.Time
}

// NewMemoryCache returns an in-memory cache holding at most maxEntries
// responses (0 means 1024). now supplies time (nil means time.Now).
func NewMemoryCache(maxEntries int, now func() time.Time) *MemoryCache {
	if maxEntries <= 0 {
		maxEntries = 1024
	}
	if now == nil {
		now = time.Now
	}
	return &MemoryCache{entries: map[string]cacheEntry{}, now: now, max: maxEntries}
}

// Get returns a copy of a live entry.
func (m *MemoryCache) Get(key string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		return nil, false
	}
	if !m.now().Before(e.expires) {
		delete(m.entries, key)
		return nil, false
	}
	return append([]byte(nil), e.value...), true
}

// Set stores a copy of value for ttl, evicting expired and then oldest entries.
func (m *MemoryCache) Set(key string, value []byte, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if len(m.entries) >= m.max {
		var oldestKey string
		var oldest time.Time
		for k, e := range m.entries {
			if !now.Before(e.expires) {
				delete(m.entries, k)
				continue
			}
			if oldestKey == "" || e.expires.Before(oldest) {
				oldestKey, oldest = k, e.expires
			}
		}
		if len(m.entries) >= m.max {
			delete(m.entries, oldestKey)
		}
	}
	m.entries[key] = cacheEntry{append([]byte(nil), value...), now.Add(ttl)}
}

// Len returns the number of stored entries, including expired ones not yet evicted.
func (m *MemoryCache) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

// keyScoped lists resources whose responses depend on the key owner.
var keyScoped = map[string]bool{"user": true, "faction": true, "company": true, "key": true}

// cacheKey identifies a request without containing a key: version, path and
// sorted query, plus a fingerprint of the key set for owner-dependent
// resources so clients with different keys never share entries.
func (c *Client) cacheKey(version, path string, q url.Values) string {
	names := make([]string, 0, len(q))
	for k := range q {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(version + " " + path + "?")
	for _, k := range names {
		b.WriteString(url.QueryEscape(k) + "=" + url.QueryEscape(strings.Join(q[k], ",")) + "&")
	}
	resource := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)[0]
	if keyScoped[resource] {
		b.WriteString("#" + c.group)
	}
	return b.String()
}

// cacheTTL is the upstream cache lifetime documented for an operation, or
// Config.CacheTTL.
func (c *Client) cacheTTL(operation string) time.Duration {
	if m, ok := operationMetadata[operation]; ok && m.CacheTTLSeconds > 0 {
		return time.Duration(m.CacheTTLSeconds) * time.Second
	}
	return c.cfg.CacheTTL
}

func keySetFingerprint(keys []string) string {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	h := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(h[:8])
}
