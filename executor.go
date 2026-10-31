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

// requestMeta carries planner knowledge into the executor.
type requestMeta struct {
	access AccessLevel
}

// DoOperation sends one endpoint operation and returns its raw JSON (P8.2).
// Reports contain successful calls only. Unknown query parameters are
// forwarded verbatim (P1.5).
func (c *Client) DoOperation(ctx context.Context, op Operation, params any, opts RequestOptions) (json.RawMessage, ExecutionReport, error) {
	meta := requestMeta{}
	if m, ok := operationMetadata[op.ID]; ok {
		meta.access = m.AccessLevel
	}
	return c.doOperation(ctx, op, params, opts, "v2", nil, false, meta)
}

// Get sends GET <BaseURL><path> with query parameters and returns raw JSON
// (P8.2). The path may contain a query string; a "key" parameter is moved to
// the Authorization header.
func (c *Client) Get(ctx context.Context, path string, query url.Values, opts RequestOptions) (json.RawMessage, ExecutionReport, error) {
	params := map[string]any{}
	if i := strings.IndexByte(path, '?'); i >= 0 {
		q, err := url.ParseQuery(path[i+1:])
		if err != nil {
			return nil, ExecutionReport{}, &ValidationError{Message: "invalid query string", Err: err}
		}
		for k, v := range q {
			params[k] = strings.Join(v, ",")
		}
		path = path[:i]
	}
	for k, v := range query {
		params[k] = strings.Join(v, ",")
	}
	return c.DoOperation(ctx, Operation{ID: "raw " + path, Path: path, Method: http.MethodGet}, params, opts)
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

func takeKey(q url.Values) string {
	key := q.Get("key")
	q.Del("key")
	return strings.TrimSpace(key)
}

func (c *Client) doOperation(ctx context.Context, op Operation, params any, opts RequestOptions, version string, selections []string, fallback bool, meta requestMeta) (json.RawMessage, ExecutionReport, error) {
	report := ExecutionReport{}
	if ctx == nil {
		return nil, report, validationf("nil context")
	}
	if opts.Attempts < 0 || opts.Timeout < 0 {
		return nil, report, validationf("request attempts and timeout must not be negative")
	}
	q, paths, e := encodeParams(params)
	if e != nil {
		return nil, report, &ValidationError{Message: "invalid parameters", Err: e}
	}
	// P2.1: a key passed as a parameter is never sent in a v2 URL.
	pinned := takeKey(q)
	for k, v := range opts.Query {
		extra, _, err := encodeParams(map[string]any{k: v})
		if err != nil {
			return nil, report, &ValidationError{Message: "invalid query option", Err: err}
		}
		if k := takeKey(extra); k != "" {
			pinned = k
		}
		for n, vs := range extra {
			q[n] = vs
		}
	}
	path := op.Path
	if path == "" || !strings.HasPrefix(path, "/") {
		return nil, report, validationf("operation path must start with slash")
	}
	for {
		start := strings.IndexByte(path, '{')
		if start < 0 {
			break
		}
		end := strings.IndexByte(path[start:], '}')
		if end < 0 {
			return nil, report, validationf("invalid path template")
		}
		end += start
		k := path[start+1 : end]
		value, ok := paths[k]
		if !ok {
			value = q.Get(k)
		}
		if value == "" {
			return nil, report, validationf("missing required path parameter: %s", k)
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
		return nil, report, validationf("only read-only GET operations are supported")
	}
	attempts := opts.Attempts
	if attempts == 0 {
		attempts = c.cfg.Attempts
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = c.cfg.Timeout
	}
	publicURL := strings.TrimRight(base, "/") + path
	if enc := q.Encode(); enc != "" {
		publicURL += "?" + enc
	}
	cacheKey := ""
	if c.cfg.Cache != nil && !opts.NoCache && pinned == "" && !op.CSV {
		cacheKey = c.cacheKey(version, path, q)
		if body, ok := c.cfg.Cache.Get(cacheKey); ok {
			report.Calls = append(report.Calls, CallReport{op.ID, "", path, version, append([]string(nil), selections...), fallback, true})
			return append(json.RawMessage(nil), body...), report, nil
		}
	}
	excluded := map[int]bool{}
	rateLimited := map[int]bool{}
	var last error
	sends, retries := 0, 0
	for {
		if e = ctx.Err(); e != nil {
			return nil, report, e
		}
		l, err := c.acquire(ctx, acquireRequest{route: opts.Route, excluded: excluded, required: meta.access, op: op.ID, pinned: pinned, last: last})
		if err != nil {
			return nil, report, err
		}
		sends++
		query := url.Values{}
		for k, v := range q {
			query[k] = append([]string(nil), v...)
		}
		if version == "v1" {
			// The legacy v1 API has no documented header authentication.
			query.Set("key", l.secret)
		}
		target := strings.TrimRight(base, "/") + path
		if enc := query.Encode(); enc != "" {
			target += "?" + enc
		}
		event := RequestEvent{Operation: op.ID, Method: method, URL: publicURL, Version: version, Attempt: sends, Route: c.pool.routes[l.route].name, KeyHint: redactKey(l.secret)}
		c.emit(c.cfg.Hooks.OnRequest, event)
		requestCtx, cancel := context.WithTimeout(ctx, timeout)
		req, err := http.NewRequestWithContext(requestCtx, method, target, nil)
		if err != nil {
			cancel()
			<-c.sem
			return nil, report, validationf("invalid request URL")
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.cfg.UserAgent)
		if version != "v1" {
			req.Header.Set("Authorization", "ApiKey "+l.secret)
		}
		if op.CSV {
			req.Header.Set("Accept", "text/csv")
		}
		started := c.cfg.Scheduler.Now()
		resp, sendErr := c.http[l.route].Do(req)
		var body []byte
		var readErr error
		if resp != nil {
			body, readErr = io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxResponseBytes+1))
			resp.Body.Close()
		}
		cancel()
		<-c.sem
		event.Duration = c.cfg.Scheduler.Now().Sub(started)
		if resp != nil {
			event.Status = resp.StatusCode
		}
		if ctx.Err() != nil {
			return nil, report, ctx.Err()
		}
		if int64(len(body)) > c.cfg.MaxResponseBytes {
			err := &TransportError{Message: "Torn response exceeds configured size limit"}
			event.Err = err
			c.emit(c.cfg.Hooks.OnError, event)
			return nil, report, err
		}
		var delay time.Duration
		retry, backoff, rotate := false, false, false
		switch {
		case sendErr != nil:
			last = &TransportError{Message: "Torn request transport failed", Cause: sanitizeTransport(sendErr)}
			retry, backoff = true, true
		case readErr != nil:
			last = &TransportError{Message: "Torn response read failed", Cause: sanitizeTransport(readErr)}
			retry, backoff = true, true
		case resp.StatusCode < 200 || resp.StatusCode >= 300:
			after := parseRetryAfter(resp.Header.Get("Retry-After"), c.cfg.Scheduler.Now())
			last = &HTTPError{StatusCode: resp.StatusCode, RetryAfter: after}
			retry = retryHTTPStatuses[resp.StatusCode]
			if after > 0 {
				delay = min(after, c.cfg.RetryAfterCap)
			} else {
				backoff = true
			}
		default:
			apiErr := c.apiError(body, op.ID, l.secret)
			if apiErr == nil {
				c.emit(c.cfg.Hooks.OnResponse, event)
				if op.CSV {
					body, _ = json.Marshal(string(body))
				} else if !json.Valid(body) {
					return nil, report, c.newDecodeError(op.ID, body, errors.New("invalid JSON"), false)
				}
				report.Calls = append(report.Calls, CallReport{op.ID, c.pool.routes[l.route].name, path, version, append([]string(nil), selections...), fallback, false})
				if cacheKey != "" {
					c.cfg.Cache.Set(cacheKey, body, c.cacheTTL(op.ID))
				}
				return json.RawMessage(body), report, nil
			}
			last = apiErr
			event.Code = apiErr.Code
			code := apiErr.TornCode()
			switch code.Action() {
			case ActionRotateKey:
				c.markKey(l, code)
				if pinned == "" {
					excluded[l.key] = true
					retry, rotate = true, true
				}
			case ActionWaitRateWindow:
				// P3.3: the key (group) waits for its window; acquire rotates to
				// another key with budget or sleeps until one frees. P2.4a: this
				// does not consume an attempt, but each key gets one code-5
				// recovery per request.
				c.markKey(l, code)
				if pinned != "" {
					retry, backoff = true, true
				} else if !rateLimited[l.key] {
					rateLimited[l.key] = true
					retry, rotate = true, true
				}
			case ActionRotateRoute:
				retry = c.blockRoute(l, opts.Route)
			case ActionRetryBackoff:
				retry, backoff = true, true
			}
		}
		event.Err = last
		if !rotate && (!retry || sends-retries >= attempts) {
			c.emit(c.cfg.Hooks.OnError, event)
			return nil, report, last
		}
		if rotate {
			// P2.4, P2.4a: failover to the next healthy key (or waiting for a
			// rate-limited key's window) does not consume an attempt.
			retries++
			event.Delay = 0
			c.emit(c.cfg.Hooks.OnRetry, event)
			continue
		}
		if backoff {
			delay = c.backoff(sends - retries)
		}
		event.Delay = delay
		c.emit(c.cfg.Hooks.OnRetry, event)
		if delay > 0 {
			if err = c.cfg.Scheduler.Sleep(ctx, delay); err != nil {
				return nil, report, err
			}
		}
	}
}

// backoff returns base*2^(n-1) capped at MaxRetryDelay, with full jitter
// unless disabled (P4.2).
func (c *Client) backoff(n int) time.Duration {
	d := c.cfg.RetryDelay
	for i := 1; i < n && d < c.cfg.MaxRetryDelay; i++ {
		d *= 2
	}
	d = min(d, c.cfg.MaxRetryDelay)
	if c.cfg.Jitter == JitterFull {
		d = time.Duration(c.cfg.Rand() * float64(d+1))
	}
	return d
}

// apiError parses a Torn error envelope; nil means the body is not an error.
func (c *Client) apiError(body []byte, operation, key string) *APIError {
	var envelope struct {
		Error *struct {
			Code    int    `json:"code"`
			Error   string `json:"error"`
			Message string `json:"message"`
		} `json:"error"`
	}
	trimmed := strings.TrimSpace(string(body))
	if !strings.HasPrefix(trimmed, "{") || !strings.Contains(trimmed[:min(len(trimmed), 64)], `"error"`) {
		return nil
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Error == nil {
		return nil
	}
	msg := envelope.Error.Error
	if msg == "" {
		msg = envelope.Error.Message
	}
	msg = c.redact(msg)
	if key != "" {
		msg = strings.ReplaceAll(msg, key, redactKey(key))
	}
	return &APIError{Code: envelope.Error.Code, Message: msg, KeyHint: redactKey(key), Operation: operation}
}

// sanitizeTransport drops the request URL (v1 URLs carry keys) while keeping
// the cause chain, e.g. context.DeadlineExceeded.
func sanitizeTransport(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func parseRetryAfter(s string, now time.Time) time.Duration {
	if n, e := strconv.Atoi(strings.TrimSpace(s)); e == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, e := http.ParseTime(s); e == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

// Execute runs a plan, merging every call's JSON. Combined selections that
// Torn rejects (code 4, HTTP 400/404/501) are split per selection; codes 22
// and 23 re-plan the call once on the other API version (P4.3).
func (c *Client) Execute(ctx context.Context, plan Plan, opts RequestOptions) (ExecutionResult, error) {
	result := ExecutionResult{Data: json.RawMessage(`{}`)}
	var run func(PlannedCall, bool) error
	run = func(call PlannedCall, rerouted bool) error {
		version := call.Version
		if version == "" {
			version = "v2"
		}
		raw, r, e := c.doOperation(ctx, call.Operation, call.Params, opts, version, call.Selections, call.Fallback, requestMeta{access: call.Access})
		if e != nil && !opts.DisableFallback {
			var api *APIError
			var httpErr *HTTPError
			isAPI := errors.As(e, &api)
			if isAPI && !rerouted && call.Resource != "" {
				switch api.TornCode() {
				case CodeOnlyAvailableInApiV1:
					if version == "v2" && len(call.Selections) <= 1 {
						return run(legacyCall(call.Resource, call.Selections, call.sourceParams()), true)
					}
				case CodeOnlyAvailableInApiV2:
					if version == "v1" {
						return run(genericCall(call.Resource, call.Selections, call.sourceParams()), true)
					}
				}
			}
			// A combined call rejected as v1-only is split so each selection
			// can be rerouted on its own.
			var splitErrs []error
			unsupported := isAPI && (api.Code == int(CodeWrongFields) || api.Code == int(CodeOnlyAvailableInApiV1) && !rerouted) || errors.As(e, &httpErr) && (httpErr.StatusCode == 400 || httpErr.StatusCode == 404 || httpErr.StatusCode == 501)
			if unsupported && len(call.Selections) > 1 {
				for _, s := range call.Selections {
					single := call
					single.Selections = []string{s}
					single.FallbackCalls = nil
					if single.Params != nil && single.Params["selections"] != nil {
						single.Params = cloneParams(call.Params)
						single.Params["selections"] = s
					}
					for _, f := range call.FallbackCalls {
						for _, selection := range f.Selections {
							if selection == s {
								single.FallbackCalls = append(single.FallbackCalls, f)
								break
							}
						}
					}
					// P4.5: every selection reports its own failure; the
					// others are still requested and merged.
					if e := run(single, rerouted); e != nil {
						splitErrs = append(splitErrs, e)
					}
				}
				return errors.Join(splitErrs...)
			}
			if unsupported && opts.AllowLegacyFallback && len(call.FallbackCalls) > 0 {
				for _, f := range call.FallbackCalls {
					f.Fallback = true
					if e := run(f, true); e != nil {
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
		if e := run(call, false); e != nil {
			return result, e
		}
	}
	return result, nil
}

func cloneParams(p map[string]any) map[string]any {
	out := make(map[string]any, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
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
