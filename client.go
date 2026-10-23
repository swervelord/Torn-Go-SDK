// Package torn provides a typed client for the Torn API.
package torn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Operation struct {
	ID, Path, Method string
	CSV              bool
}
type Scheduler interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
}
type systemScheduler struct{}

func (systemScheduler) Now() time.Time { return time.Now() }
func (systemScheduler) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type Route struct {
	Name      string
	ProxyURL  string            `json:"-"`
	Transport http.RoundTripper `json:"-"`
}

func (r Route) String() string   { return "Route(" + r.Name + ")" }
func (r Route) GoString() string { return r.String() }

type RouteUsage struct {
	Route    string
	KeyIndex int
	Time     time.Time
}
type Config struct {
	APIKeys                                                []string `json:"-"`
	BaseURL, LegacyBaseURL                                 string
	UserAgent                                              string
	HTTPClient                                             *http.Client `json:"-"`
	Routes                                                 []Route
	RouteOrder                                             []string
	MaxConcurrency, Attempts, KeyPerMinute, RoutePerMinute int
	MaxResponseBytes                                       int64
	Timeout, RetryDelay                                    time.Duration
	Scheduler                                              Scheduler        `json:"-"`
	Observer                                               func(RouteUsage) `json:"-"`
}

func (c Config) String() string {
	return fmt.Sprintf("Config{keys:%d, routes:%d}", len(c.APIKeys), len(c.Routes))
}
func (c Config) GoString() string { return c.String() }

// ConfigFromEnv reads comma-separated TORN_API_KEYS (or TORN_API_KEY), and optional
// TORN_BASE_URL, TORN_TIMEOUT, TORN_ATTEMPTS and TORN_CONCURRENCY.
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

type RequestOptions struct {
	Attempts            int
	Timeout             time.Duration
	Route               string
	DisableFallback     bool
	AllowLegacyFallback bool
	Query               map[string]any
}
type CallReport struct {
	OperationID, Route, Path, Version string
	Selections                        []string
	Fallback                          bool
}
type ExecutionReport struct{ Calls []CallReport }
type ExecutionResult struct {
	Data   json.RawMessage
	Report ExecutionReport
}
type APIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return fmt.Sprintf("Torn API error %d: %s", e.Code, e.Message) }

type HTTPError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string { return fmt.Sprintf("Torn HTTP status %d", e.StatusCode) }

type TransportError struct{ Message string }

func (e *TransportError) Error() string { return e.Message }

type routeState struct {
	name   string
	client *http.Client
}
type quotaBucket struct {
	minute int64
	count  int
}
type Client struct {
	cfg               Config
	routes            []routeState
	sem               chan struct{}
	mu                sync.Mutex
	nextKey           int
	keys, routeQuotas []quotaBucket
}

func (c *Client) String() string               { return "Torn Client" }
func (c *Client) GoString() string             { return c.String() }
func (c *Client) MarshalJSON() ([]byte, error) { return []byte(`{"client":"torn"}`), nil }
func NewClient(cfg Config) (*Client, error) {
	if len(cfg.APIKeys) == 0 {
		return nil, errors.New("at least one API key is required")
	}
	uniqueKeys := make([]string, 0, len(cfg.APIKeys))
	seenKeys := make(map[string]bool, len(cfg.APIKeys))
	for _, k := range cfg.APIKeys {
		k = strings.TrimSpace(k)
		if k == "" {
			return nil, errors.New("API keys must not be empty")
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
			return nil, errors.New("invalid API base URL")
		}
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "Torn-Go-SDK/1.0"
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = 250 * time.Millisecond
	}
	if cfg.Attempts == 0 {
		cfg.Attempts = 3
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
	if cfg.Scheduler == nil {
		cfg.Scheduler = systemScheduler{}
	}
	if cfg.Timeout < 0 || cfg.RetryDelay < 0 || cfg.Attempts < 1 || cfg.MaxConcurrency < 1 || cfg.KeyPerMinute < 1 || cfg.RoutePerMinute < 1 || cfg.MaxResponseBytes < 1 || cfg.MaxResponseBytes == int64(^uint64(0)>>1) {
		return nil, errors.New("configuration limits must be positive")
	}
	c := &Client{cfg: cfg, sem: make(chan struct{}, cfg.MaxConcurrency), keys: make([]quotaBucket, len(cfg.APIKeys))}
	routes := append([]Route(nil), cfg.Routes...)
	if len(routes) == 0 {
		routes = []Route{{Name: "default"}}
	}
	seen := map[string]bool{}
	for _, r := range routes {
		if r.Name == "" || seen[r.Name] {
			return nil, errors.New("route names must be nonempty and unique")
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
				return nil, errors.New("invalid proxy URL")
			}
			tr := http.DefaultTransport.(*http.Transport).Clone()
			tr.Proxy = http.ProxyURL(u)
			hc.Transport = tr
		}
		hc.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
		c.routes = append(c.routes, routeState{r.Name, &hc})
	}
	if len(cfg.RouteOrder) > 0 {
		ordered := []routeState{}
		used := map[string]bool{}
		for _, n := range cfg.RouteOrder {
			if used[n] {
				return nil, errors.New("duplicate route order entry")
			}
			found := false
			for _, r := range c.routes {
				if r.name == n {
					ordered = append(ordered, r)
					found = true
					used[n] = true
					break
				}
			}
			if !found {
				return nil, errors.New("unknown route in route order")
			}
		}
		for _, r := range c.routes {
			if !used[r.name] {
				ordered = append(ordered, r)
			}
		}
		c.routes = ordered
	}
	c.routeQuotas = make([]quotaBucket, len(c.routes))
	return c, nil
}
func (c *Client) reserve(ctx context.Context, route string) (int, int, error) {
	for {
		if e := ctx.Err(); e != nil {
			return 0, 0, e
		}
		select {
		case c.sem <- struct{}{}:
		case <-ctx.Done():
			return 0, 0, ctx.Err()
		}
		now := c.cfg.Scheduler.Now()
		minute := now.Unix() / 60
		c.mu.Lock()
		for i := range c.keys {
			if c.keys[i].minute != minute {
				c.keys[i] = quotaBucket{minute: minute}
			}
		}
		for i := range c.routeQuotas {
			if c.routeQuotas[i].minute != minute {
				c.routeQuotas[i] = quotaBucket{minute: minute}
			}
		}
		ri := -1
		known := route == ""
		for i, r := range c.routes {
			if route != "" && r.name != route {
				continue
			}
			known = true
			if c.routeQuotas[i].count < c.cfg.RoutePerMinute {
				ri = i
				break
			}
		}
		ki := -1
		for i := range c.keys {
			j := (c.nextKey + i) % len(c.keys)
			if c.keys[j].count < c.cfg.KeyPerMinute {
				ki = j
				break
			}
		}
		if ri >= 0 && ki >= 0 {
			c.keys[ki].count++
			c.routeQuotas[ri].count++
			c.nextKey = (ki + 1) % len(c.keys)
			c.mu.Unlock()
			if c.cfg.Observer != nil {
				c.cfg.Observer(RouteUsage{c.routes[ri].name, ki, now})
			}
			return ki, ri, nil
		}
		c.mu.Unlock()
		<-c.sem
		if !known {
			return 0, 0, errors.New("unknown request route")
		}
		wait := time.Unix((minute+1)*60, 0).Sub(now)
		if e := c.cfg.Scheduler.Sleep(ctx, wait); e != nil {
			return 0, 0, e
		}
	}
}
func Decode[T any](raw json.RawMessage) (T, error) {
	var v T
	e := json.Unmarshal(raw, &v)
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
func Call[T any](ctx context.Context, c *Client, op Operation, params any, opts RequestOptions) (T, ExecutionReport, error) {
	raw, r, e := c.DoOperation(ctx, op, params, opts)
	if e != nil {
		var z T
		return z, r, e
	}
	v, e := Decode[T](raw)
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
