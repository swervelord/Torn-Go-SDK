// Package torntest is a consumer test kit for the Torn SDK (PARITY_SPEC
// P8.5): a virtual clock that satisfies torn.Scheduler, a scripted
// http.RoundTripper that records requests, and fixture loading.
//
//	clock := torntest.NewClock(time.Unix(0, 0))
//	mock := torntest.NewTransport(torntest.JSON(200, map[string]any{"profile": map[string]any{"id": 1}}))
//	client, _ := torn.NewClient(torn.Config{
//		APIKeys:    []string{"test-key"},
//		HTTPClient: &http.Client{Transport: mock},
//		Scheduler:  clock,
//		Jitter:     torn.JitterNone,
//	})
package torntest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Clock is a virtual torn.Scheduler: Sleep advances time instantly.
type Clock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

// NewClock returns a clock starting at start.
func NewClock(start time.Time) *Clock { return &Clock{now: start} }

// Now returns the virtual time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Sleep advances the virtual time by d unless ctx is already done.
func (c *Clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > 0 {
		c.now = c.now.Add(d)
	}
	c.sleeps = append(c.sleeps, d)
	return nil
}

// Advance moves the clock forward without recording a sleep.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Sleeps returns every requested sleep in order.
func (c *Clock) Sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

// Response is one scripted reply. Err simulates a network failure.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
	Err    error
}

// JSON returns a response whose body is v encoded as JSON (a string or
// []byte body is sent verbatim).
func JSON(status int, v any) Response {
	var body []byte
	switch b := v.(type) {
	case []byte:
		body = b
	case string:
		body = []byte(b)
	default:
		var err error
		if body, err = json.Marshal(v); err != nil {
			panic(err)
		}
	}
	return Response{Status: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}
}

// TornError returns a Torn error envelope {"error":{"code":code,"error":message}}.
func TornError(code int, message string) Response {
	return JSON(200, map[string]any{"error": map[string]any{"code": code, "error": message}})
}

// NetworkError returns a response that fails as a transport error.
func NetworkError() Response { return Response{Err: errors.New("torntest: simulated network failure")} }

// Request is a recorded request.
type Request struct {
	Method string
	URL    *url.URL
	Header http.Header
	// At is the clock time of the send when the transport has a Clock.
	At time.Time
}

// Auth returns the key the request authenticated with: the Authorization
// "ApiKey" header (v2) or the key query parameter (v1).
func (r Request) Auth() string {
	if h := r.Header.Get("Authorization"); h != "" {
		return strings.TrimPrefix(h, "ApiKey ")
	}
	return r.URL.Query().Get("key")
}

// String describes the request without its key.
func (r Request) String() string {
	q := r.URL.Query()
	q.Del("key")
	return r.Method + " " + r.URL.Path + "?" + q.Encode()
}

// GoString describes the request without its key.
func (r Request) GoString() string { return r.String() }

// Format describes the request without its key for every fmt verb.
func (r Request) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(r.String())) }

// Transport is a scripted http.RoundTripper. Responses are returned in order;
// Handler, when set, answers instead. It records every request.
type Transport struct {
	mu        sync.Mutex
	responses []Response
	requests  []Request
	// Handler answers requests when set.
	Handler func(*http.Request) Response
	// Clock stamps recorded requests when set.
	Clock *Clock
}

// NewTransport returns a transport answering with responses in order.
func NewTransport(responses ...Response) *Transport {
	return &Transport{responses: responses}
}

// Push appends scripted responses.
func (t *Transport) Push(responses ...Response) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.responses = append(t.responses, responses...)
}

// RoundTrip records the request and returns the next scripted response.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	rec := Request{Method: req.Method, URL: req.URL, Header: req.Header.Clone()}
	if t.Clock != nil {
		rec.At = t.Clock.Now()
	}
	t.requests = append(t.requests, rec)
	var r Response
	switch {
	case t.Handler != nil:
		h := t.Handler
		t.mu.Unlock()
		r = h(req)
		t.mu.Lock()
	case len(t.responses) > 0:
		r = t.responses[0]
		t.responses = t.responses[1:]
	default:
		t.mu.Unlock()
		return nil, fmt.Errorf("torntest: unexpected request %s", rec)
	}
	t.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	if r.Status == 0 {
		r.Status = 200
	}
	h := r.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	return &http.Response{StatusCode: r.Status, Status: http.StatusText(r.Status), Header: h, Body: io.NopCloser(bytes.NewReader(r.Body)), Request: req}, nil
}

// Requests returns the recorded requests.
func (t *Transport) Requests() []Request {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Request(nil), t.requests...)
}

// Remaining returns the number of unused scripted responses.
func (t *Transport) Remaining() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.responses)
}

// AssertSent fails tb unless exactly n requests were sent and every scripted
// response was used.
func (t *Transport) AssertSent(tb testing.TB, n int) {
	tb.Helper()
	if got := len(t.Requests()); got != n || t.Remaining() != 0 {
		tb.Fatalf("torntest: sent %d requests (want %d), %d scripted responses unused", got, n, t.Remaining())
	}
}

// LoadFixture reads a JSON fixture and verifies it is valid JSON.
func LoadFixture(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("torntest: %s is not valid JSON", path)
	}
	return b, nil
}

// MustFixture loads a fixture or fails tb.
func MustFixture(tb testing.TB, path string) []byte {
	tb.Helper()
	b, err := LoadFixture(path)
	if err != nil {
		tb.Fatal(err)
	}
	return b
}
