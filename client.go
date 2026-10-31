package torn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Operation identifies one HTTP operation: an official operation ID (or a
// descriptive ID for raw calls), a path relative to the API version base, the
// method (GET only) and whether the response is CSV text.
type Operation struct {
	ID, Path, Method string
	CSV              bool
}

// Scheduler supplies time to the client. Inject a virtual scheduler (see
// package torntest) to make rate limits, retries and backoff deterministic.
type Scheduler interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
}
type systemScheduler struct{}

func (systemScheduler) Now() time.Time { return time.Now() }
func (systemScheduler) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Route is a named egress path (for example one proxy per IP) with its own
// per-minute budget (P3.4). ProxyURL replaces the transport of HTTPClient and
// Transport for this route.
type Route struct {
	Name      string
	ProxyURL  string            `json:"-"`
	Transport http.RoundTripper `json:"-"`
}

func (r Route) String() string { return "Route(" + r.Name + ")" }

// GoString hides the proxy URL, which may contain credentials.
func (r Route) GoString() string { return r.String() }

// RouteUsage is reported to Config.Observer for every reserved send.
// KeyIndex is -1 for a key pinned through a deprecated Params.Key field.
type RouteUsage struct {
	Route    string
	KeyIndex int
	Time     time.Time
}

// JitterMode selects backoff randomization (P4.2).
type JitterMode string

const (
	// JitterFull sleeps a uniformly random duration in [0, backoff]. It is the default.
	JitterFull JitterMode = "full"
	// JitterNone sleeps exactly base*2^(attempt-1), capped; used by deterministic tests.
	JitterNone JitterMode = "none"
)

// Config configures a Client. The zero value of every field selects the
// documented default. Config never prints its keys: String, GoString and
// JSON encoding redact them (P2.2).
type Config struct {
	// APIKeys is the key pool, used round-robin over healthy keys (P2.3).
	APIKeys []string `json:"-"`
	// BaseURL is the v2 base (default https://api.torn.com/v2); LegacyBaseURL
	// is the v1 base (default https://api.torn.com).
	BaseURL, LegacyBaseURL string
	// UserAgent defaults to "Torn-Go-SDK/<SDKVersion>".
	UserAgent string
	// HTTPClient is copied per route; redirects are never followed.
	HTTPClient *http.Client `json:"-"`
	// Routes and RouteOrder configure egress routes (default: one direct route).
	Routes     []Route
	RouteOrder []string
	// MaxConcurrency limits in-flight requests (default 8). Attempts is the
	// maximum number of sends per request (default DefaultMaxAttempts).
	// KeyPerMinute is the sliding-window budget per key group (default 100)
	// and RoutePerMinute per route (default 1000).
	MaxConcurrency, Attempts, KeyPerMinute, RoutePerMinute int
	// MaxResponseBytes caps a response body (default 64 MiB).
	MaxResponseBytes int64
	// Timeout is the per-attempt timeout (default 30s). RetryDelay is the
	// backoff base delay (default DefaultRetryBaseDelay).
	Timeout, RetryDelay time.Duration
	// MaxRetryDelay caps backoff (default DefaultRetryMaxDelay) and
	// RetryAfterCap caps a server Retry-After (default DefaultRetryAfterCap).
	MaxRetryDelay, RetryAfterCap time.Duration
	// Jitter selects backoff randomization (default JitterFull).
	Jitter JitterMode
	// Scheduler supplies time and sleeps (default: the system clock).
	Scheduler Scheduler `json:"-"`
	// Observer is called for every reserved send.
	Observer func(RouteUsage) `json:"-"`
	// KeyGroups declares keys owned by the same Torn user; each group shares
	// one rate-limit budget because Torn limits per user (P3.2).
	KeyGroups [][]string `json:"-"`
	// DetectKeyOwners groups keys by owner when Client.KeyInfo runs (P3.2).
	DetectKeyOwners bool
	// CheckAccess rejects requests before sending when every key's known
	// access level is too low (P6.2). Levels come from KeyAccess or RefreshKeyInfo.
	CheckAccess bool
	// KeyAccess declares known key access levels, keyed by key.
	KeyAccess map[string]AccessLevel `json:"-"`
	// StrictDecoding validates typed responses strictly before decoding:
	// unknown enum values, undocumented properties and override-relaxed
	// fields become *DecodeError (P1.3). Default decoding is tolerant.
	StrictDecoding bool
	// Hooks observe requests, responses, retries and errors (P8.3).
	Hooks Hooks `json:"-"`
	// Cache enables response caching (P8.4); nil disables it. CacheTTL is
	// the lifetime for operations without documented upstream cache metadata
	// (default 30s).
	Cache    Cache `json:"-"`
	CacheTTL time.Duration
	// Rand returns values in [0,1) for jitter (default math/rand/v2).
	Rand func() float64 `json:"-"`
}

func (c Config) String() string {
	return fmt.Sprintf("Config{keys:%d, routes:%d}", len(c.APIKeys), len(c.Routes))
}

// GoString redacts keys like String.
func (c Config) GoString() string { return c.String() }

// Format redacts keys for every fmt verb.
func (c Config) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(c.String())) }

// ConfigFromEnv reads TORN_API_KEYS (comma-separated) or else TORN_API_KEY (P2.6),
// TORN_BASE_URL / TORN_API_BASE_URL_V2 / TORN_API_BASE_URL_V1,
// TORN_USER_AGENT, TORN_TIMEOUT (Go duration), TORN_HTTP_TIMEOUT_SECS,
// TORN_NETWORK_RETRY_BACKOFF_MS, TORN_ATTEMPTS / TORN_MAX_ATTEMPTS,
// TORN_CONCURRENCY / TORN_MAX_IN_FLIGHT, TORN_RATE_LIMIT_PER_KEY_PER_MIN and
// TORN_RATE_LIMIT_PER_IP_PER_MIN.
func ConfigFromEnv() (Config, error) {
	c := Config{BaseURL: os.Getenv("TORN_API_BASE_URL_V2"), LegacyBaseURL: os.Getenv("TORN_API_BASE_URL_V1"), UserAgent: os.Getenv("TORN_USER_AGENT")}
	if c.BaseURL == "" {
		c.BaseURL = os.Getenv("TORN_BASE_URL")
	}
	keys := os.Getenv("TORN_API_KEYS")
	if keys == "" {
		keys = os.Getenv("TORN_API_KEY")
	}
	for _, k := range strings.Split(keys, ",") {
		if k = strings.TrimSpace(k); k != "" {
			c.APIKeys = append(c.APIKeys, k)
		}
	}
	if v := os.Getenv("TORN_TIMEOUT"); v != "" {
		d, e := time.ParseDuration(v)
		if e != nil || d <= 0 {
			return c, errors.New("invalid TORN_TIMEOUT")
		}
		c.Timeout = d
	}
	for _, p := range []struct {
		name  string
		unit  time.Duration
		value *time.Duration
	}{
		{"TORN_HTTP_TIMEOUT_SECS", time.Second, &c.Timeout},
		{"TORN_NETWORK_RETRY_BACKOFF_MS", time.Millisecond, &c.RetryDelay},
	} {
		if s := os.Getenv(p.name); s != "" {
			n, e := strconv.ParseInt(s, 10, 64)
			if e != nil || n < 1 || n > int64((1<<63-1)/p.unit) {
				return c, fmt.Errorf("invalid %s", p.name)
			}
			*p.value = time.Duration(n) * p.unit
		}
	}
	for _, p := range []struct {
		name string
		v    *int
	}{{"TORN_ATTEMPTS", &c.Attempts}, {"TORN_CONCURRENCY", &c.MaxConcurrency}, {"TORN_MAX_ATTEMPTS", &c.Attempts}, {"TORN_MAX_IN_FLIGHT", &c.MaxConcurrency}, {"TORN_RATE_LIMIT_PER_KEY_PER_MIN", &c.KeyPerMinute}, {"TORN_RATE_LIMIT_PER_IP_PER_MIN", &c.RoutePerMinute}} {
		if s := os.Getenv(p.name); s != "" {
			n, e := strconv.Atoi(s)
			if e != nil || n < 1 {
				return c, fmt.Errorf("invalid %s", p.name)
			}
			*p.v = n
		}
	}
	return c, nil
}

// RequestOptions adjusts one call.
type RequestOptions struct {
	// Attempts overrides Config.Attempts; Timeout overrides Config.Timeout.
	Attempts int
	Timeout  time.Duration
	// Route pins the request to one named egress route.
	Route string
	// DisableFallback turns off splitting rejected selection combinations
	// and automatic v1/v2 rerouting (codes 22/23).
	DisableFallback bool
	// AllowLegacyFallback retries selections on API v1 when v2 rejects them
	// as unsupported (code 4 or HTTP 400/404/501).
	AllowLegacyFallback bool
	// Query adds query parameters forwarded verbatim. A "key" entry is sent
	// in the Authorization header on v2, never in the URL (P2.1).
	Query map[string]any
	// NoCache bypasses Config.Cache for this call.
	NoCache bool
	// Strict validates this call's typed response strictly (see Config.StrictDecoding).
	Strict bool
}

// CallReport describes one successful HTTP call.
type CallReport struct {
	OperationID, Route, Path, Version string
	Selections                        []string
	Fallback                          bool
	// Cached is true when the response came from Config.Cache.
	Cached bool
}

// ExecutionReport lists the successful calls behind a result.
type ExecutionReport struct{ Calls []CallReport }

// ExecutionResult is the merged raw JSON of a plan plus its report.
type ExecutionResult struct {
	Data   json.RawMessage
	Report ExecutionReport
}

// Client is a Torn API client. It is safe for concurrent use; key, route and
// rate-limit state is shared by every goroutine using it (P3.5).
type Client struct {
	cfg   Config
	sem   chan struct{}
	mu    sync.Mutex
	pool  keyPool
	http  []*http.Client
	group string
}

func (c *Client) String() string { return "Torn Client" }

// GoString hides keys.
func (c *Client) GoString() string { return c.String() }

// Format hides keys for every fmt verb.
func (c *Client) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(c.String())) }

// MarshalJSON hides keys.
func (c *Client) MarshalJSON() ([]byte, error) { return []byte(`{"client":"torn"}`), nil }

// NewClient validates cfg, applies defaults and builds a client.
func NewClient(cfg Config) (*Client, error) {
	if len(cfg.APIKeys) == 0 {
		return nil, validationf("at least one API key is required")
	}
	uniqueKeys := make([]string, 0, len(cfg.APIKeys))
	seenKeys := make(map[string]bool, len(cfg.APIKeys))
	for _, k := range cfg.APIKeys {
		k = strings.TrimSpace(k)
		if k == "" {
			return nil, validationf("API keys must not be empty")
		}
		if !seenKeys[k] {
			uniqueKeys = append(uniqueKeys, k)
			seenKeys[k] = true
		}
	}
	cfg.APIKeys = uniqueKeys
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.torn.com/v2"
	}
	if cfg.LegacyBaseURL == "" {
		cfg.LegacyBaseURL = "https://api.torn.com"
	}
	for _, s := range []string{cfg.BaseURL, cfg.LegacyBaseURL} {
		u, e := url.Parse(s)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" {
			return nil, validationf("invalid API base URL")
		}
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "Torn-Go-SDK/" + SDKVersion
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = DefaultRetryBaseDelay
	}
	if cfg.MaxRetryDelay == 0 {
		cfg.MaxRetryDelay = DefaultRetryMaxDelay
	}
	if cfg.RetryAfterCap == 0 {
		cfg.RetryAfterCap = DefaultRetryAfterCap
	}
	if cfg.Jitter == "" {
		cfg.Jitter = JitterFull
	}
	if cfg.Jitter != JitterFull && cfg.Jitter != JitterNone {
		return nil, validationf("unknown jitter mode %q", cfg.Jitter)
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Float64
	}
	if cfg.Attempts == 0 {
		cfg.Attempts = DefaultMaxAttempts
	}
	if cfg.MaxConcurrency == 0 {
		cfg.MaxConcurrency = 8
	}
	if cfg.KeyPerMinute == 0 {
		cfg.KeyPerMinute = 100
	}
	if cfg.RoutePerMinute == 0 {
		cfg.RoutePerMinute = 1000
	}
	if cfg.MaxResponseBytes == 0 {
		cfg.MaxResponseBytes = 64 << 20
	}
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = 30 * time.Second
	}
	if cfg.Scheduler == nil {
		cfg.Scheduler = systemScheduler{}
	}
	if cfg.Timeout < 0 || cfg.RetryDelay < 0 || cfg.MaxRetryDelay < 0 || cfg.RetryAfterCap < 0 || cfg.CacheTTL < 0 || cfg.Attempts < 1 || cfg.MaxConcurrency < 1 || cfg.KeyPerMinute < 1 || cfg.RoutePerMinute < 1 || cfg.MaxResponseBytes < 1 || cfg.MaxResponseBytes == int64(^uint64(0)>>1) {
		return nil, validationf("configuration limits must be positive")
	}
	c := &Client{cfg: cfg, sem: make(chan struct{}, cfg.MaxConcurrency)}
	c.pool.keys = make([]keyEntry, len(cfg.APIKeys))
	groupOf := map[string]int{}
	for gi, group := range cfg.KeyGroups {
		for _, k := range group {
			k = strings.TrimSpace(k)
			if !seenKeys[k] {
				return nil, validationf("key group %d names a key that is not configured", gi)
			}
			if _, dup := groupOf[k]; dup {
				return nil, validationf("a key appears in more than one key group")
			}
			groupOf[k] = gi
		}
	}
	firstOfGroup := map[int]int{}
	for i, k := range cfg.APIKeys {
		g := len(c.pool.groups)
		if gi, ok := groupOf[k]; ok {
			if existing, seen := firstOfGroup[gi]; seen {
				g = existing
			} else {
				firstOfGroup[gi] = g
				c.pool.groups = append(c.pool.groups, window{})
			}
		} else {
			c.pool.groups = append(c.pool.groups, window{})
		}
		access := cfg.KeyAccess[k]
		if access != "" && !access.IsKnown() {
			return nil, validationf("unknown access level %q", access)
		}
		c.pool.keys[i] = keyEntry{key: k, group: g, status: KeyHealthy, access: access}
	}
	routes := append([]Route(nil), cfg.Routes...)
	if len(routes) == 0 {
		routes = []Route{{Name: "default"}}
	}
	seen := map[string]bool{}
	type built struct {
		name   string
		client *http.Client
	}
	var all []built
	for _, r := range routes {
		if r.Name == "" || seen[r.Name] {
			return nil, validationf("route names must be nonempty and unique")
		}
		seen[r.Name] = true
		hc := http.Client{}
		if cfg.HTTPClient != nil {
			hc = *cfg.HTTPClient
		}
		if r.Transport != nil {
			hc.Transport = r.Transport
		}
		if r.ProxyURL != "" {
			u, e := url.Parse(r.ProxyURL)
			if e != nil || u.Host == "" {
				return nil, validationf("invalid proxy URL")
			}
			tr := http.DefaultTransport.(*http.Transport).Clone()
			tr.Proxy = http.ProxyURL(u)
			hc.Transport = tr
		}
		hc.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
		all = append(all, built{r.Name, &hc})
	}
	if len(cfg.RouteOrder) > 0 {
		ordered := []built{}
		used := map[string]bool{}
		for _, n := range cfg.RouteOrder {
			if used[n] {
				return nil, validationf("duplicate route order entry")
			}
			found := false
			for _, r := range all {
				if r.name == n {
					ordered = append(ordered, r)
					found = true
					used[n] = true
					break
				}
			}
			if !found {
				return nil, validationf("unknown route in route order")
			}
		}
		for _, r := range all {
			if !used[r.name] {
				ordered = append(ordered, r)
			}
		}
		all = ordered
	}
	for _, r := range all {
		c.pool.routes = append(c.pool.routes, routeEntry{name: r.name})
		c.http = append(c.http, r.client)
	}
	c.group = keySetFingerprint(cfg.APIKeys)
	return c, nil
}

// Decode unmarshals raw into T. Generated models decode tolerantly (P1.3);
// failures are *models.DecodeError values with a JSON path.
func Decode[T any](raw json.RawMessage) (T, error) {
	var v T
	e := decodeModel(raw, &v)
	return v, e
}

// DecodeWith lets consumers supply contract-specific decoding without unsafe casts.
func DecodeWith[T any](raw json.RawMessage, decoder func(json.RawMessage) (T, error)) (T, error) {
	if decoder == nil {
		var zero T
		return zero, errors.New("nil custom decoder")
	}
	return decoder(raw)
}

// Call sends an operation and decodes its response into T (P8.2). Decode
// failures are *DecodeError values carrying the raw body.
func Call[T any](ctx context.Context, c *Client, op Operation, params any, opts RequestOptions) (T, ExecutionReport, error) {
	var v T
	raw, r, e := c.DoOperation(ctx, op, params, opts)
	if e != nil {
		return v, r, e
	}
	e = c.decodeResponse(op.ID, "", raw, &v, opts)
	return v, r, e
}
func encodeParams(params any) (url.Values, map[string]string, error) {
	q := url.Values{}
	p := map[string]string{}
	if params == nil {
		return q, p, nil
	}
	v := reflect.ValueOf(params)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return q, p, nil
		}
		v = v.Elem()
	}
	var paramErr error
	add := func(k string, v reflect.Value, isPath bool) {
		for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
			if v.IsNil() {
				return
			}
			v = v.Elem()
		}
		if !v.IsValid() {
			return
		}
		if m, ok := v.Interface().(json.Marshaler); ok {
			data, err := m.MarshalJSON()
			if err != nil {
				paramErr = errors.New("invalid encoded parameter: " + k)
				return
			}
			var scalar any
			dec := json.NewDecoder(bytes.NewReader(data))
			dec.UseNumber()
			if err = dec.Decode(&scalar); err != nil {
				paramErr = errors.New("invalid encoded parameter: " + k)
				return
			}
			if scalar == nil {
				return
			}
			s := ""
			switch x := scalar.(type) {
			case string:
				s = x
			case json.Number:
				s = x.String()
			case bool:
				s = strconv.FormatBool(x)
			case []any:
				parts := []string{}
				for _, v := range x {
					parts = append(parts, fmt.Sprint(v))
				}
				s = strings.Join(parts, ",")
			default:
				paramErr = errors.New("parameter must encode as scalar or array: " + k)
				return
			}
			if isPath {
				p[k] = s
			} else {
				q.Set(k, s)
			}
			return
		}
		if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
			parts := []string{}
			for i := 0; i < v.Len(); i++ {
				encoded, err := queryScalar(v.Index(i).Interface())
				if err != nil {
					paramErr = errors.New("invalid array query parameter: " + k)
					return
				}
				parts = append(parts, encoded)
			}
			q.Set(k, strings.Join(parts, ","))
			return
		}
		s := fmt.Sprint(v.Interface())
		if isPath {
			p[k] = s
		} else {
			q.Set(k, s)
		}
	}
	switch v.Kind() {
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return nil, nil, errors.New("parameter map keys must be strings")
		}
		it := v.MapRange()
		for it.Next() {
			k := it.Key().String()
			add(k, it.Value(), false)
			if q.Has(k) {
				p[k] = q.Get(k)
			}
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			if k := strings.Split(f.Tag.Get("path"), ",")[0]; k != "" && k != "-" {
				add(k, v.Field(i), true)
				continue
			}
			k := strings.Split(f.Tag.Get("query"), ",")[0]
			if k == "-" {
				continue
			}
			if k == "" {
				k = strings.Split(f.Tag.Get("json"), ",")[0]
			}
			if k == "-" {
				continue
			}
			if k == "" {
				k = strings.ToLower(f.Name)
			}
			add(k, v.Field(i), false)
		}
	default:
		return nil, nil, errors.New("parameters must be a struct or string-keyed map")
	}
	return q, p, paramErr
}

func queryScalar(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var wire any
	if err = dec.Decode(&wire); err != nil {
		return "", err
	}
	switch v := wire.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case bool:
		return strconv.FormatBool(v), nil
	case nil:
		return "", errors.New("null query value")
	default:
		return "", errors.New("query value must be scalar")
	}
}
