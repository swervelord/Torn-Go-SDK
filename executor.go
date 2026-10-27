package torn

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DoOperation sends one endpoint operation. Reports contain successful calls only.
func (c *Client) DoOperation(ctx context.Context, op Operation, params any, opts RequestOptions) (json.RawMessage, ExecutionReport, error) {
	return c.doOperation(ctx, op, params, opts, "v2", nil, false)
}

// DoCSV retrieves the exact text returned by a CSV operation.
func (c *Client) DoCSV(ctx context.Context, op Operation, params any, opts RequestOptions) (string, ExecutionReport, error) {
	op.CSV = true
	raw, report, err := c.DoOperation(ctx, op, params, opts)
	if err != nil {
		return "", report, err
	}
	text, err := Decode[string](raw)
	return text, report, err
}
func (c *Client) doOperation(ctx context.Context, op Operation, params any, opts RequestOptions, version string, selections []string, fallback bool) (json.RawMessage, ExecutionReport, error) {
	report := ExecutionReport{}
	if ctx == nil {
		return nil, report, errors.New("nil context")
	}
	if opts.Attempts < 0 || opts.Timeout < 0 {
		return nil, report, errors.New("request attempts and timeout must not be negative")
	}
	q, paths, e := encodeParams(params)
	if e != nil {
		return nil, report, e
	}
	for k, v := range opts.Query {
		extra, _, err := encodeParams(map[string]any{k: v})
		if err != nil {
			return nil, report, err
		}
		for n, vs := range extra {
			q[n] = vs
		}
	}
	path := op.Path
	if path == "" || !strings.HasPrefix(path, "/") {
		return nil, report, errors.New("operation path must start with slash")
	}
	for {
		start := strings.IndexByte(path, '{')
		if start < 0 {
			break
		}
		end := strings.IndexByte(path[start:], '}')
		if end < 0 {
			return nil, report, errors.New("invalid path template")
		}
		end += start
		k := path[start+1 : end]
		value, ok := paths[k]
		if !ok {
			value = q.Get(k)
		}
		if value == "" {
			return nil, report, errors.New("missing required path parameter: " + k)
		}
		q.Del(k)
		path = path[:start] + url.PathEscape(value) + path[end+1:]
	}
	if len(selections) > 0 && q.Has("selections") {
		q.Set("selections", strings.Join(selections, ","))
	}
	base := c.cfg.BaseURL
	if version == "v1" {
		base = c.cfg.LegacyBaseURL
	}
	method := op.Method
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet {
		return nil, report, errors.New("only read-only GET operations are supported")
	}
	attempts := opts.Attempts
	if attempts == 0 {
		attempts = c.cfg.Attempts
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = c.cfg.Timeout
	}
	var last error
	for attempt := 1; attempt <= attempts; attempt++ {
		if e = ctx.Err(); e != nil {
			return nil, report, e
		}
		key, route, err := c.reserve(ctx, opts.Route)
		if err != nil {
			return nil, report, err
		}
		query := url.Values{}
		for k, v := range q {
			query[k] = append([]string(nil), v...)
		}
		query.Set("key", c.cfg.APIKeys[key])
		target := strings.TrimRight(base, "/") + path + "?" + query.Encode()
		requestCtx, cancel := context.WithTimeout(ctx, timeout)
		req, err := http.NewRequestWithContext(requestCtx, method, target, nil)
		if err != nil {
			cancel()
			<-c.sem
			return nil, report, errors.New("invalid request URL")
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.cfg.UserAgent)
		if op.CSV {
			req.Header.Set("Accept", "text/csv")
		}
		resp, sendErr := c.routes[route].client.Do(req)
		var body []byte
		var readErr error
		if resp != nil {
			body, readErr = io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxResponseBytes+1))
			resp.Body.Close()
		}
		cancel()
		<-c.sem
		if ctx.Err() != nil {
			return nil, report, ctx.Err()
		}
		if int64(len(body)) > c.cfg.MaxResponseBytes {
			return nil, report, &TransportError{Message: "Torn response exceeds configured size limit"}
		}
		delay := time.Duration(attempt) * c.cfg.RetryDelay
		retry := false
		if sendErr != nil {
			last = &TransportError{Message: "Torn request transport failed"}
			retry = true
		} else if readErr != nil {
			last = &TransportError{Message: "Torn response read failed"}
			retry = true
		} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			after := parseRetryAfter(resp.Header.Get("Retry-After"), c.cfg.Scheduler.Now())
			last = &HTTPError{StatusCode: resp.StatusCode, RetryAfter: after}
			retry = resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode == 500 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504
			if after > delay {
				delay = after
			}
		} else {
			var envelope struct {
				Error *struct {
					Code    int    `json:"code"`
					Error   string `json:"error"`
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(body, &envelope)
			if envelope.Error != nil {
				msg := envelope.Error.Error
				if msg == "" {
					msg = envelope.Error.Message
				}
				for _, secret := range c.cfg.APIKeys {
					msg = strings.ReplaceAll(msg, secret, "[REDACTED]")
				}
				last = &APIError{Code: envelope.Error.Code, Message: msg}
				retry = envelope.Error.Code == 5 || envelope.Error.Code == 8 || envelope.Error.Code == 9
				if envelope.Error.Code == 5 {
					c.exhaust(key, route, mentionsIP(msg))
					now := c.cfg.Scheduler.Now()
					until := time.Unix((now.Unix()/60+1)*60, 0).Sub(now)
					if until > delay {
						delay = until
					}
				}
			} else {
				if op.CSV {
					body, _ = json.Marshal(string(body))
				} else if !json.Valid(body) {
					return nil, report, errors.New("Torn returned invalid JSON")
				}
				report.Calls = append(report.Calls, CallReport{op.ID, c.routes[route].name, path, version, append([]string(nil), selections...), fallback})
				return json.RawMessage(body), report, nil
			}
		}
		if !retry || attempt == attempts {
			return nil, report, last
		}
		if err = c.cfg.Scheduler.Sleep(ctx, delay); err != nil {
			return nil, report, err
		}
	}
	return nil, report, last
}
func parseRetryAfter(s string, now time.Time) time.Duration {
	if n, e := strconv.Atoi(s); e == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, e := http.ParseTime(s); e == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}
func (c *Client) Execute(ctx context.Context, plan Plan, opts RequestOptions) (ExecutionResult, error) {
	result := ExecutionResult{Data: json.RawMessage(`{}`)}
	var run func(PlannedCall) error
	run = func(call PlannedCall) error {
		version := call.Version
		if version == "" {
			version = "v2"
		}
		raw, r, e := c.doOperation(ctx, call.Operation, call.Params, opts, version, call.Selections, call.Fallback)
		if e != nil && !opts.DisableFallback {
			var api *APIError
			var httpErr *HTTPError
			isAPI := errors.As(e, &api)
			unsupported := isAPI && api.Code == 4 || errors.As(e, &httpErr) && (httpErr.StatusCode == 400 || httpErr.StatusCode == 404 || httpErr.StatusCode == 501)
			if (unsupported || isAPI && rejectsCombination(api.Message)) && len(call.Selections) > 1 {
				for _, s := range call.Selections {
					single := call
					single.Selections = []string{s}
					single.FallbackCalls = nil
					for _, f := range call.FallbackCalls {
						for _, selection := range f.Selections {
							if selection == s {
								single.FallbackCalls = append(single.FallbackCalls, f)
								break
							}
						}
					}
					if e := run(single); e != nil {
						return e
					}
				}
				return nil
			}
			if (unsupported || isAPI && requestsV1(api.Message)) && opts.AllowLegacyFallback && len(call.FallbackCalls) > 0 {
				for _, f := range call.FallbackCalls {
					f.Fallback = true
					if e := run(f); e != nil {
						return e
					}
				}
				return nil
			}
		}
		if e != nil {
			return e
		}
		merged, e := MergeJSON(result.Data, raw)
		if e != nil {
			return e
		}
		result.Data = merged
		result.Report.Calls = append(result.Report.Calls, r.Calls...)
		return nil
	}
	for _, call := range plan.Calls {
		if e := run(call); e != nil {
			return result, e
		}
	}
	return result, nil
}

// MergeJSON recursively merges objects. Later scalars, nulls and arrays replace earlier values.
// Raw messages preserve integer precision and avoid conversion through float64.
func MergeJSON(a, b json.RawMessage) (json.RawMessage, error) {
	if !json.Valid(a) || !json.Valid(b) {
		return nil, errors.New("cannot merge invalid JSON")
	}
	var left, right map[string]json.RawMessage
	ea := json.Unmarshal(a, &left)
	eb := json.Unmarshal(b, &right)
	if ea != nil || eb != nil || left == nil || right == nil {
		return append(json.RawMessage(nil), b...), nil
	}
	for k, v := range right {
		if old, ok := left[k]; ok {
			merged, e := MergeJSON(old, v)
			if e != nil {
				return nil, e
			}
			left[k] = merged
		} else {
			left[k] = v
		}
	}
	return json.Marshal(left)
}

// exhaust spends the rest of this minute's local budget for a key that Torn
// rate limited (code 5), and for its route when the limit is attributed to the
// IP, so concurrent and later calls rotate instead of repeating the rejection.
func (c *Client) exhaust(key, route int, ip bool) {
	minute := c.cfg.Scheduler.Now().Unix() / 60
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys[key] = quotaBucket{minute: minute, count: c.cfg.KeyPerMinute}
	if ip {
		c.routeQuotas[route] = quotaBucket{minute: minute, count: c.cfg.RoutePerMinute}
	}
}

// mentionsIP matches "IP" as a whole word so messages such as "multiple" do not
// spend a route.
func mentionsIP(message string) bool {
	for _, word := range strings.FieldsFunc(message, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	}) {
		if strings.EqualFold(word, "ip") {
			return true
		}
	}
	return false
}

// rejectsCombination recognizes Torn messages that reject a selection
// combination independent of the error code; the call is split per selection.
func rejectsCombination(message string) bool {
	m := strings.ToLower(message)
	return strings.Contains(m, "selection") && strings.Contains(m, "cannot") || strings.Contains(m, "cannot be used together") || strings.Contains(m, "standalone")
}

// requestsV1 recognizes Torn messages directing a v2 selection to API v1, which
// makes opt-in legacy fallback eligible.
func requestsV1(message string) bool {
	m := strings.ToLower(message)
	return strings.Contains(m, "not available in api v2") || strings.Contains(m, "fallback to api v1") || strings.Contains(m, "fall back to api v1") || strings.Contains(m, "api v1") && strings.Contains(m, "selection")
}
