package torn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeScheduler struct {
	mu    sync.Mutex
	now   time.Time
	waits []time.Duration
}

func (f *fakeScheduler) Now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
func (f *fakeScheduler) Sleep(ctx context.Context, d time.Duration) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waits = append(f.waits, d)
	f.now = f.now.Add(d)
	return nil
}
func testClient(t *testing.T, h http.HandlerFunc, edit func(*Config)) (*Client, *httptest.Server) {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	cfg := Config{APIKeys: []string{"private-test-key"}, BaseURL: s.URL, LegacyBaseURL: s.URL}
	if edit != nil {
		edit(&cfg)
	}
	c, e := NewClient(cfg)
	if e != nil {
		t.Fatal(e)
	}
	return c, s
}
func TestRuntimeParameterEncoding(t *testing.T) {
	type parameters struct {
		ID         string   `path:"id"`
		From       *int64   `query:"from"`
		Selections []string `query:"selections"`
		Absent     *bool    `query:"absent"`
	}
	n := int64(9007199254740993)
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/user/a%2Fb" || r.URL.Query().Get("from") != "9007199254740993" || r.URL.Query().Get("selections") != "profile,job" || r.URL.Query().Has("absent") {
			t.Errorf("unexpected request %s", r.URL.Redacted())
		}
		fmt.Fprint(w, `{"ok":true}`)
	}, nil)
	_, r, e := c.DoOperation(context.Background(), Operation{ID: "sample", Path: "/user/{id}"}, parameters{"a/b", &n, []string{"profile", "job"}, nil}, RequestOptions{})
	if e != nil || len(r.Calls) != 1 {
		t.Fatalf("%v %+v", e, r)
	}
}
func TestRuntimeMergePrecision(t *testing.T) {
	got, e := MergeJSON(json.RawMessage(`{"a":{"x":1,"y":2},"large":9007199254740993,"list":[1]}`), json.RawMessage(`{"a":{"x":null},"list":[2]}`))
	if e != nil || string(got) != `{"a":{"x":null,"y":2},"large":9007199254740993,"list":[2]}` {
		t.Fatalf("%s %v", got, e)
	}
}
func TestRuntimeRetryAndSuccessReport(t *testing.T) {
	var count atomic.Int32
	clock := &fakeScheduler{now: time.Unix(120, 0)}
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if count.Add(1) < 3 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{}`)
	}, func(c *Config) { c.Scheduler = clock })
	_, r, e := c.DoOperation(context.Background(), Operation{Path: "/user"}, nil, RequestOptions{})
	if e != nil || count.Load() != 3 || len(r.Calls) != 1 || len(clock.waits) != 2 || clock.waits[0] != 2*time.Second {
		t.Fatalf("retry result %v %d %+v %+v", e, count.Load(), r, clock.waits)
	}
}
func TestRuntimeNoRetryAuthenticationError(t *testing.T) {
	var count atomic.Int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		fmt.Fprint(w, `{"error":{"code":2,"error":"invalid private-test-key"}}`)
	}, nil)
	_, r, e := c.DoOperation(context.Background(), Operation{Path: "/key"}, nil, RequestOptions{})
	var api *APIError
	if !errors.As(e, &api) || count.Load() != 1 || len(r.Calls) != 0 || strings.Contains(e.Error(), "private-test-key") {
		t.Fatalf("%v %+v", e, r)
	}
}
func TestRuntimeQuotaRotationAndSpillover(t *testing.T) {
	clock := &fakeScheduler{now: time.Unix(179, 0)}
	var usage []RouteUsage
	var keys []string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.URL.Query().Get("key"))
		fmt.Fprint(w, `{}`)
	}, func(c *Config) {
		c.Scheduler = clock
		c.APIKeys = []string{"a", "b"}
		c.KeyPerMinute = 1
		c.RoutePerMinute = 1
		c.Routes = []Route{{Name: "one"}, {Name: "two"}}
		c.Observer = func(u RouteUsage) { usage = append(usage, u) }
	})
	for i := 0; i < 3; i++ {
		if _, _, e := c.DoOperation(context.Background(), Operation{Path: "/user"}, nil, RequestOptions{}); e != nil {
			t.Fatal(e)
		}
	}
	if strings.Join(keys, ",") != "a,b,a" || usage[0].Route != "one" || usage[1].Route != "two" || len(clock.waits) != 1 || clock.waits[0] != time.Second {
		t.Fatalf("%v %+v %v", keys, usage, clock.waits)
	}
}
func TestRuntimeCancellationAndConcurrency(t *testing.T) {
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	var active, peak atomic.Int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		defer active.Add(-1)
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		fmt.Fprint(w, `{}`)
	}, func(c *Config) { c.MaxConcurrency = 1 })
	done := make(chan error, 1)
	go func() {
		_, _, e := c.DoOperation(context.Background(), Operation{Path: "/user"}, nil, RequestOptions{})
		done <- e
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, e := c.DoOperation(ctx, Operation{Path: "/user"}, nil, RequestOptions{})
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if len(c.sem) != 0 || peak.Load() != 1 {
		t.Fatalf("permit leak or concurrency %d %d", len(c.sem), peak.Load())
	}
}
func TestRuntimeCSVAndCredentialSafety(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "a,b\n1,2\n") }, nil)
	data, report, e := c.DoOperation(context.Background(), Operation{Path: "/csv", CSV: true}, nil, RequestOptions{})
	text, e2 := Decode[string](data)
	if e != nil || e2 != nil || text != "a,b\n1,2\n" || len(report.Calls) != 1 {
		t.Fatalf("%q %v %v", text, e, e2)
	}
	for _, v := range []any{c, c.cfg, c.cfg.Routes} {
		s := fmt.Sprintf("%+v %#v", v, v)
		if strings.Contains(s, "private-test-key") {
			t.Fatal("credential in formatting")
		}
		b, _ := json.Marshal(v)
		if strings.Contains(string(b), "private-test-key") {
			t.Fatal("credential in JSON")
		}
	}
}
func TestRuntimeSplitAndLegacyFallback(t *testing.T) {
	var calls []string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		s := r.URL.Query().Get("selections")
		calls = append(calls, r.URL.Path+":"+s)
		if strings.Contains(s, ",") {
			fmt.Fprint(w, `{"error":{"code":4,"error":"invalid combined selection"}}`)
			return
		}
		fmt.Fprintf(w, `{%q:{"value":1}}`, s)
	}, nil)
	p := Plan{Calls: []PlannedCall{{Operation: Operation{Path: "/user"}, Selections: []string{"profile", "job"}, Params: map[string]any{"selections": "profile,job"}, Version: "v2"}}}
	r, e := c.Execute(context.Background(), p, RequestOptions{})
	if e != nil || len(r.Report.Calls) != 2 || strings.Join(calls, "|") != "/user:profile,job|/user:profile|/user:job" {
		t.Fatalf("%v %+v %v", e, r, calls)
	}
}
func TestRuntimeDirectOmitsSelections(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("selections") {
			t.Error("direct endpoint received selections")
		}
		fmt.Fprint(w, `{}`)
	}, nil)
	_, e := c.Execute(context.Background(), Plan{Calls: []PlannedCall{{Operation: Operation{Path: "/user/profile"}, Selections: []string{"profile"}}}}, RequestOptions{})
	if e != nil {
		t.Fatal(e)
	}
}
func TestRuntimeTransportCancellation(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, e := c.DoOperation(ctx, Operation{Path: "/user"}, nil, RequestOptions{})
	if !errors.Is(e, context.DeadlineExceeded) || len(c.sem) != 0 {
		t.Fatalf("%v permit count %d", e, len(c.sem))
	}
}
func TestRuntimeOptionsValidation(t *testing.T) {
	if _, e := NewClient(Config{}); e == nil {
		t.Fatal("accepted missing credentials")
	}
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) }, nil)
	for _, o := range []RequestOptions{{Attempts: -1}, {Timeout: -1}, {Route: "missing"}} {
		if _, _, e := c.DoOperation(context.Background(), Operation{Path: "/x"}, nil, o); e == nil {
			t.Fatalf("accepted invalid options %+v", o)
		}
	}
}

type blockingScheduler struct{ entered chan struct{} }

func (b *blockingScheduler) Now() time.Time { return time.Unix(121, 0) }
func (b *blockingScheduler) Sleep(ctx context.Context, d time.Duration) error {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}
func TestRuntimeCancellationDuringQuotaAndBackoff(t *testing.T) {
	for _, quota := range []bool{true, false} {
		t.Run(fmt.Sprint(quota), func(t *testing.T) {
			scheduler := &blockingScheduler{entered: make(chan struct{}, 1)}
			c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if !quota {
					w.WriteHeader(503)
					return
				}
				fmt.Fprint(w, `{}`)
			}, func(c *Config) {
				c.Scheduler = scheduler
				if quota {
					c.KeyPerMinute = 1
				}
			})
			if quota {
				if _, _, e := c.DoOperation(context.Background(), Operation{Path: "/user"}, nil, RequestOptions{}); e != nil {
					t.Fatal(e)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { _, _, e := c.DoOperation(ctx, Operation{Path: "/user"}, nil, RequestOptions{}); done <- e }()
			<-scheduler.entered
			cancel()
			if e := <-done; !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
			if len(c.sem) != 0 {
				t.Fatal("permit leaked")
			}
		})
	}
}
func TestRuntimeLegacyFallbackControl(t *testing.T) {
	var paths []string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/v2/user" {
			fmt.Fprint(w, `{"error":{"code":4,"error":"unavailable"}}`)
			return
		}
		fmt.Fprint(w, `{"profile":{"id":1}}`)
	}, nil)
	p := Plan{Calls: []PlannedCall{{Operation: Operation{Path: "/v2/user"}, Selections: []string{"profile"}, Params: map[string]any{"selections": "profile"}, FallbackCalls: []PlannedCall{{Operation: Operation{Path: "/user"}, Version: "v1", Selections: []string{"profile"}, Params: map[string]any{"selections": "profile"}}}}}}
	if _, e := c.Execute(context.Background(), p, RequestOptions{}); e == nil || len(paths) != 1 {
		t.Fatalf("unexpected default fallback %v %v", e, paths)
	}
	result, e := c.Execute(context.Background(), p, RequestOptions{AllowLegacyFallback: true})
	if e != nil || len(result.Report.Calls) != 1 || !result.Report.Calls[0].Fallback || result.Report.Calls[0].Version != "v1" {
		t.Fatalf("%v %+v", e, result)
	}
}
func TestRuntimeRedirectDoesNotExposeCredential(t *testing.T) {
	var leaked atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer destination.Close()
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"?key=private-test-key", http.StatusFound)
	}, nil)
	_, _, e := c.DoOperation(context.Background(), Operation{Path: "/user"}, nil, RequestOptions{})
	var status *HTTPError
	if !errors.As(e, &status) || status.StatusCode != 302 || leaked.Load() {
		t.Fatalf("redirect behavior %v leaked %v", e, leaked.Load())
	}
}

func TestRuntimeRejectsOversizedJSONAndCSV(t *testing.T) {
	for _, csv := range []bool{false, true} {
		t.Run(fmt.Sprint(csv), func(t *testing.T) {
			var sends atomic.Int32
			c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { sends.Add(1); fmt.Fprint(w, `{"oversized":true}`) }, func(c *Config) { c.MaxResponseBytes = 8 })
			data, report, e := c.DoOperation(context.Background(), Operation{Path: "/large", CSV: csv}, nil, RequestOptions{})
			var transport *TransportError
			if !errors.As(e, &transport) || data != nil || len(report.Calls) != 0 || sends.Load() != 1 {
				t.Fatalf("unexpected oversized response behavior data=%s report=%+v err=%v sends=%d", data, report, e, sends.Load())
			}
			if len(c.sem) != 0 {
				t.Fatal("permit leak")
			}
		})
	}
}

func TestRuntimeQuotaWaitDoesNotBlockOtherRoutes(t *testing.T) {
	scheduler := &blockingScheduler{entered: make(chan struct{}, 1)}
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) }, func(c *Config) {
		c.Scheduler = scheduler
		c.RoutePerMinute = 1
		c.MaxConcurrency = 1
		c.Routes = []Route{{Name: "one"}, {Name: "two"}}
	})
	op := Operation{Path: "/user"}
	if _, _, e := c.DoOperation(context.Background(), op, nil, RequestOptions{Route: "one"}); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, e := c.DoOperation(ctx, op, nil, RequestOptions{Route: "one"}); done <- e }()
	<-scheduler.entered
	otherCtx, otherCancel := context.WithTimeout(context.Background(), time.Second)
	defer otherCancel()
	if _, _, e := c.DoOperation(otherCtx, op, nil, RequestOptions{Route: "two"}); e != nil {
		t.Fatalf("available route blocked by unrelated quota wait: %v", e)
	}
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func FuzzRuntimeMerge(f *testing.F) {
	f.Add(`{"a":1}`, `{"a":null}`)
	f.Add(`9007199254740993`, `[]`)
	f.Fuzz(func(t *testing.T, a, b string) {
		got, e := MergeJSON(json.RawMessage(a), json.RawMessage(b))
		if e == nil && !json.Valid(got) {
			t.Fatal("invalid merged JSON")
		}
	})
}
