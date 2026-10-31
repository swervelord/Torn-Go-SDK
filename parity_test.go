package torn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/swervelord/Torn-Go-SDK/models"
	"github.com/swervelord/Torn-Go-SDK/torntest"
)

const parityKey = "PARITYSECRET0001"

func parityClient(t *testing.T, mock *torntest.Transport, mutate func(*Config)) (*Client, *torntest.Clock) {
	t.Helper()
	clock := torntest.NewClock(time.Unix(1_700_000_000, 0))
	mock.Clock = clock
	cfg := Config{APIKeys: []string{parityKey}, HTTPClient: &http.Client{Transport: mock}, Scheduler: clock, Jitter: JitterNone}
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c, clock
}

func TestDecodeErrorCarriesPathValueAndRawBody(t *testing.T) {
	body := `{"travel":{"destination":"Torn","method":null,"departed_at":null,"arrival_at":null,"time_left":"soon"}}`
	mock := torntest.NewTransport(torntest.JSON(200, body))
	c, _ := parityClient(t, mock, nil)
	_, _, err := c.GetMyTravelInformation(context.Background(), GetMyTravelInformationParams{})
	var d *DecodeError
	if !errors.As(err, &d) || d.Operation != "getMyTravelInformation" || d.Path != "$.travel.time_left" || d.Expected != "integer" || d.Value != `"soon"` || string(d.Raw()) != body {
		t.Fatalf("%#v %v", d, err)
	}
	var md *models.DecodeError
	if !errors.As(err, &md) || !IsDecodeError(err) {
		t.Fatal("model decode error not wrapped")
	}
	// Array indexes are located on the error path only.
	var items models.TornItemsResponse
	err = json.Unmarshal([]byte(`{"items":[{"id":1},{"id":"x"}]}`), &items)
	if err == nil || !strings.Contains(err.Error(), "$.items[0]") && !strings.Contains(err.Error(), "$.items[") {
		t.Fatalf("array path: %v", err)
	}
}

func TestStrictDecodingIsOptIn(t *testing.T) {
	body := `{"profile":{"id":1,"name":"Test","level":10,"gender":"Futuregender","status":{"description":"Okay","details":null,"state":"Okay","color":"green","until":null},"brand_new":1}}`
	mock := torntest.NewTransport(torntest.JSON(200, body), torntest.JSON(200, body))
	c, _ := parityClient(t, mock, nil)
	got, _, err := c.GetMyBasicInformation(context.Background(), GetMyBasicInformationParams{})
	if err != nil || got.Profile.Gender.IsKnown() || got.Profile.Gender != "Futuregender" || string(got.Profile.AdditionalProperties["brand_new"]) != "1" {
		t.Fatalf("tolerant decode: %+v %v", got.Profile, err)
	}
	_, _, err = c.GetMyBasicInformation(context.Background(), GetMyBasicInformationParams{}, RequestOptions{Strict: true})
	var d *DecodeError
	if !errors.As(err, &d) || !d.Strict {
		t.Fatalf("strict decode: %v", err)
	}
}

func TestSpecOverridesRelaxTradeItems(t *testing.T) {
	var trade models.UserTrade
	raw := []byte(`{"id":1,"trader":{"id":1,"name":"a"},"user":{"id":2,"name":"b"},"description":"x","completed_at":1,"expires_at":1,"modified_at":1}`)
	if err := json.Unmarshal(raw, &trade); err != nil || trade.Items != nil {
		t.Fatalf("UserTrade without items: %v", err)
	}
	if err := models.ValidateStrict("UserTrade", raw); err == nil || !strings.Contains(err.Error(), "spec-overrides") {
		t.Fatalf("strict override report: %v", err)
	}
}

func TestUnionDiscriminator(t *testing.T) {
	var r models.UserJobResponse
	for raw, want := range map[string]string{`{"job":null}`: "null", `{"job":{"type":"job","name":"Army","position":"Private"}}`: "UserJob"} {
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			t.Fatal(err)
		}
		if r.Job.WhichName() != want || !r.Job.Matches(r.Job.Which()) {
			t.Fatalf("%s: %d %s", raw, r.Job.Which(), r.Job.WhichName())
		}
	}
	var empty models.UserJobResponseJob
	if empty.Which() != 0 || empty.WhichName() != "" {
		t.Fatal("empty union")
	}
}

func TestErrorCodeTableAndHelpers(t *testing.T) {
	if len(ErrorCodes()) != 34 || !CodeBackendError.IsKnown() || ErrorCode(99).IsKnown() || ErrorCode(99).Action() != ActionFail {
		t.Fatal("error code table")
	}
	if CodeIncorrectKey.String() != "incorrect_key(2)" || CodeIpBlocked.Action() != ActionRotateRoute || CodeDailyReadLimitReached.DisablesKey() != "until_utc_midnight" {
		t.Fatal("error code policy")
	}
	api := func(code ErrorCode) error { return &APIError{Code: int(code)} }
	cases := []struct {
		err                                     error
		retryable, key, rate, access, transient bool
	}{
		{api(CodeBackendError), true, false, false, false, true},
		{api(CodeTooManyRequests), true, false, true, false, false},
		{api(CodeIpBlocked), true, false, true, false, false},
		{api(CodeIncorrectKey), false, true, false, false, false},
		{&NoUsableKeysError{Last: api(CodeIncorrectKey)}, false, true, false, false, false},
		{api(CodeAccessLevelTooLow), false, false, false, true, false},
		{&AccessError{Required: AccessLimited}, false, false, false, true, false},
		{&HTTPError{StatusCode: 503}, true, false, false, false, true},
		{&HTTPError{StatusCode: 429}, true, false, true, false, true},
		{&TransportError{Message: "x", Cause: context.DeadlineExceeded}, true, false, false, false, true},
		{api(CodeIncorrectId), false, false, false, false, false},
	}
	for i, tc := range cases {
		if IsRetryable(tc.err) != tc.retryable || IsKeyError(tc.err) != tc.key || IsRateLimit(tc.err) != tc.rate || IsAccessError(tc.err) != tc.access || IsTransient(tc.err) != tc.transient {
			t.Errorf("case %d %v", i, tc.err)
		}
	}
	if code, ok := Code(&NoUsableKeysError{Last: api(CodeIncorrectKey)}); !ok || code != CodeIncorrectKey {
		t.Fatal("Code through wrapper")
	}
}

func TestPerAttemptTimeoutUnwrapsToDeadlineExceeded(t *testing.T) {
	block := &torntest.Transport{Handler: func(r *http.Request) torntest.Response {
		<-r.Context().Done()
		return torntest.Response{Err: r.Context().Err()}
	}}
	c, _ := parityClient(t, block, func(c *Config) { c.Timeout = 10 * time.Millisecond; c.Attempts = 1 })
	_, _, err := c.DoOperation(context.Background(), Operation{Path: "/user"}, nil, RequestOptions{})
	var tr *TransportError
	if !errors.As(err, &tr) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%T %v", err, err)
	}
}

func TestDeprecatedParamsKeyGoesToHeader(t *testing.T) {
	mock := torntest.NewTransport(torntest.JSON(200, `{"timestamp":1}`))
	c, _ := parityClient(t, mock, nil)
	other := "OTHERKEY00000002"
	_, _, err := c.GetForumTimestamp(context.Background(), GetForumTimestampParams{Key: &other})
	if err != nil {
		t.Fatal(err)
	}
	req := mock.Requests()[0]
	if req.URL.Query().Has("key") || req.Header.Get("Authorization") != "ApiKey "+other {
		t.Fatalf("key placement: %s %q", req.URL, req.Header.Get("Authorization"))
	}
}

func TestCode8RotatesRouteAndCode14DisablesUntilMidnight(t *testing.T) {
	mock := torntest.NewTransport(torntest.TornError(8, "IP block"), torntest.JSON(200, `{}`))
	var routes []string
	c, _ := parityClient(t, mock, func(c *Config) {
		c.Routes = []Route{{Name: "a"}, {Name: "b"}}
		c.Observer = func(u RouteUsage) { routes = append(routes, u.Route) }
	})
	if _, _, err := c.DoOperation(context.Background(), Operation{Path: "/user"}, nil, RequestOptions{}); err != nil || strings.Join(routes, ",") != "a,b" {
		t.Fatalf("%v %v", err, routes)
	}
	mock = torntest.NewTransport(torntest.TornError(14, "daily"), torntest.JSON(200, `{}`))
	c, clock := parityClient(t, mock, func(c *Config) { c.APIKeys = []string{"KEYAAAAAAAAAAAA1", "KEYBBBBBBBBBBBB2"} })
	if _, _, err := c.DoOperation(context.Background(), Operation{Path: "/user"}, nil, RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	s := c.KeyStates()[0]
	u := clock.Now().UTC()
	if s.Status != KeyDisabled || !s.Until.Equal(time.Date(u.Year(), u.Month(), u.Day()+1, 0, 0, 0, 0, time.UTC)) || s.Reason != "daily_read_limit_reached(14)" {
		t.Fatalf("%+v", s)
	}
	clock.Advance(25 * time.Hour)
	if c.KeyStates()[0].Status != KeyHealthy {
		t.Fatal("code 14 key did not recover after midnight")
	}
}

func TestFullJitterStaysWithinBackoff(t *testing.T) {
	c, _ := parityClient(t, torntest.NewTransport(), func(c *Config) { c.Jitter = JitterFull; c.Rand = func() float64 { return 0.999 } })
	for n, max := range map[int]time.Duration{1: 500 * time.Millisecond, 2: time.Second, 10: 30 * time.Second, 40: 30 * time.Second} {
		if d := c.backoff(n); d < 0 || d > max {
			t.Fatalf("attempt %d delay %v > %v", n, d, max)
		}
	}
	c.cfg.Rand = func() float64 { return 0 }
	if c.backoff(3) != 0 {
		t.Fatal("full jitter lower bound")
	}
}

func TestTypedPagerItemsAndPrev(t *testing.T) {
	page := func(ids []int, next, prev string) torntest.Response {
		news := []any{}
		for _, id := range ids {
			news = append(news, json.RawMessage(fmt.Sprintf(`{"id":"%d","text":"t","timestamp":%d}`, id, id)))
		}
		links := map[string]any{"next": nil, "prev": nil}
		if next != "" {
			links["next"] = next
		}
		if prev != "" {
			links["prev"] = prev
		}
		return torntest.JSON(200, map[string]any{"news": news, "_metadata": map[string]any{"links": links}})
	}
	mock := torntest.NewTransport(page([]int{1, 2}, "https://api.torn.com/v2/faction/news?to=2&key=LEAK", ""), page([]int{3}, "", "https://api.torn.com/v2/faction/news?from=3"))
	c, _ := parityClient(t, mock, nil)
	pager := c.GetMyFactionNewsPager(GetMyFactionNewsParams{})
	ctx := context.Background()
	first, ok, err := pager.Next(ctx)
	if err != nil || !ok || first.Number != 1 || strings.Contains(first.Next, "LEAK") || first.Next != "https://api.torn.com/v2/faction/news?to=2" {
		t.Fatalf("%+v %v %v", first, ok, err)
	}
	second, ok, err := pager.Next(ctx)
	if err != nil || !ok || second.Number != 2 || second.Prev == "" {
		t.Fatalf("%v %v", ok, err)
	}
	if _, ok, _ := pager.Next(ctx); ok {
		t.Fatal("pager continued past the last page")
	}
	mock.Push(page([]int{1}, "", ""))
	if back, ok, err := pager.Prev(ctx); err != nil || !ok || back.Number != 3 {
		t.Fatalf("prev: %v %v", ok, err)
	}
	if mock.Requests()[1].URL.Query().Has("key") || mock.Requests()[2].URL.Query().Get("from") != "3" {
		t.Fatal("link query")
	}
	// Typed items across pages, honoring MaxItems.
	mock = torntest.NewTransport(page([]int{1, 2}, "https://api.torn.com/v2/faction/news?to=2", ""), page([]int{3, 4}, "", ""))
	c, _ = parityClient(t, mock, nil)
	var ids []string
	for item, err := range c.GetMyFactionNewsItems(ctx, GetMyFactionNewsParams{}, PageOptions{MaxItems: 3}) {
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, item.ID)
	}
	if fmt.Sprint(ids) != "[1 2 3]" {
		t.Fatalf("items %v", ids)
	}
}

func TestHooksAndCacheAreRedactedAndSkipErrors(t *testing.T) {
	mock := torntest.NewTransport(torntest.TornError(17, "backend"), torntest.JSON(200, `{"timestamp":1}`), torntest.TornError(6, "bad id"), torntest.TornError(6, "bad id"))
	var events []string
	var payloads []RequestEvent
	record := func(kind string) func(RequestEvent) {
		return func(e RequestEvent) { events = append(events, kind); payloads = append(payloads, e) }
	}
	cache := NewMemoryCache(0, nil)
	c, clock := parityClient(t, mock, func(c *Config) {
		c.Hooks = Hooks{OnRequest: record("request"), OnResponse: record("response"), OnRetry: record("retry"), OnError: record("error")}
		c.Cache = cache
	})
	cache.now = clock.Now
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, report, err := c.DoOperation(ctx, Operation{ID: "getTimestamp", Path: "/torn/timestamp"}, nil, RequestOptions{}); err != nil || i == 1 && !report.Calls[0].Cached {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if strings.Join(events, ",") != "request,retry,request,response" {
		t.Fatalf("events %v", events)
	}
	for _, e := range payloads {
		if strings.Contains(fmt.Sprintf("%+v", e), parityKey) || e.KeyHint != "***0001" {
			t.Fatalf("hook leaked key: %+v", e)
		}
	}
	// Errors are never cached.
	for i := 0; i < 2; i++ {
		if _, _, err := c.DoOperation(ctx, Operation{Path: "/torn/x"}, nil, RequestOptions{}); err == nil {
			t.Fatal("error expected")
		}
	}
	mock.AssertSent(t, 4)
	clock.Advance(31 * time.Second)
	if _, ok := cache.Get(c.cacheKey("v2", "/torn/timestamp", nil)); ok {
		t.Fatal("cache entry outlived its TTL")
	}
}

func TestSecretsNeverFormatted(t *testing.T) {
	mock := torntest.NewTransport(torntest.TornError(2, "Incorrect key "+parityKey))
	c, _ := parityClient(t, mock, func(c *Config) { c.KeyAccess = map[string]AccessLevel{parityKey: AccessFull} })
	_, _, err := c.DoOperation(context.Background(), Operation{Path: "/user"}, nil, RequestOptions{})
	var none *NoUsableKeysError
	if !errors.As(err, &none) {
		t.Fatalf("%v", err)
	}
	decodeErr := c.newDecodeError("op", []byte(`{}`), errors.New("x "+parityKey), false)
	secret := parityKey
	opts := RequestOptions{Query: map[string]any{"key": parityKey, "comment": "x"}}
	params := GetForumTimestampParams{Key: &secret}
	values := []any{err, c.cfg, &c.cfg, c, c.KeyStates(), c.KeyStates()[0], RequestEvent{KeyHint: redactKey(parityKey)}, decodeErr, Route{Name: "r", ProxyURL: "http://user:" + parityKey + "@proxy"}, opts, &opts, params, &params, []any{params, opts}}
	for _, s := range formatAll(values...) {
		if strings.Contains(s, parityKey) {
			t.Fatalf("secret in %q", s)
		}
	}
	// Redaction keeps the useful parts and still sends the key in the header.
	if !strings.Contains(fmt.Sprint(opts), "comment:x") || !strings.Contains(fmt.Sprint(params), "Key:***0001") {
		t.Fatalf("%v / %v", opts, params)
	}
	if b, _ := json.Marshal(params); string(b) != `{}` {
		t.Fatalf("params JSON %s", b)
	}
}

func TestAccessLevelsAndKeyInfo(t *testing.T) {
	if !AccessFull.Satisfies(AccessLimited) || AccessPublic.Satisfies(AccessMinimal) || !AccessCustom.Satisfies(AccessFull) || AccessLevelFromKeyType("Limited Access") != AccessLimited {
		t.Fatal("access ordering")
	}
	if l, ok := SelectionAccessLevel("user", "travelinventory"); !ok || l != AccessLimited {
		t.Fatal(l)
	}
	if l, ok := OperationAccessLevel("getMyTravelInventory"); !ok || l != AccessLimited {
		t.Fatal(l)
	}
	info := func(owner int, typ string) torntest.Response {
		return torntest.JSON(200, fmt.Sprintf(`{"info":{"selections":{"company":[],"faction":[],"market":[],"property":[],"torn":[],"user":[],"racing":[],"forum":[],"key":[]},"access":{"level":1,"type":%q,"faction":false,"company":false,"log":{"custom_permissions":false,"available":[]}},"user":{"id":%d,"faction_id":null,"company_id":null}}}`, typ, owner))
	}
	mock := torntest.NewTransport(info(7, "Public Only"), info(7, "Full Access"))
	c, _ := parityClient(t, mock, func(c *Config) {
		c.APIKeys = []string{"KEYAAAAAAAAAAAA1", "KEYBBBBBBBBBBBB2"}
		c.DetectKeyOwners = true
		c.CheckAccess = true
	})
	infos, err := c.RefreshKeyInfo(context.Background())
	if err != nil || infos[0].Access != AccessPublic || infos[1].Access != AccessFull || infos[0].OwnerID != 7 {
		t.Fatalf("%+v %v", infos, err)
	}
	states := c.KeyStates()
	if states[0].Group != states[1].Group || states[0].Access != AccessPublic {
		t.Fatalf("owner grouping %+v", states)
	}
	if a := mock.Requests()[1].Header.Get("Authorization"); a != "ApiKey KEYBBBBBBBBBBBB2" {
		t.Fatal(a)
	}
	// CheckAccess skips the public key for a limited selection.
	mock.Push(torntest.JSON(200, `{"travelinventory":{"capacity":{"base":1,"special":0},"inventory":[]}}`))
	if _, err := c.RawSelections(context.Background(), "user", []string{"travelinventory"}, nil, RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	if a := mock.Requests()[2].Header.Get("Authorization"); a != "ApiKey KEYBBBBBBBBBBBB2" {
		t.Fatal("check access chose an insufficient key")
	}
}

func TestV1CommentAndUnknownFiltersForwarded(t *testing.T) {
	mock := torntest.NewTransport(torntest.JSON(200, `{"weapons":[]}`))
	c, _ := parityClient(t, mock, nil)
	if _, err := c.RawSelections(context.Background(), "faction", []string{"weapons"}, map[string]any{"comment": "bot", "newfilter": "x"}, RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	q := mock.Requests()[0].URL.Query()
	if q.Get("comment") != "bot" || q.Get("newfilter") != "x" || q.Get("key") != parityKey {
		t.Fatal(q)
	}
}

func TestDecodeOperationAndRawGet(t *testing.T) {
	v, err := DecodeOperation("getMyTravelInventory", []byte(`{"travelinventory":{"capacity":{"base":1,"special":0},"inventory":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(*GetMyTravelInventoryResponse); !ok {
		t.Fatalf("%T", v)
	}
	mock := torntest.NewTransport(torntest.JSON(200, `{"x":1}`))
	c, _ := parityClient(t, mock, nil)
	if raw, _, err := c.Get(context.Background(), "/torn/future?cat=a&key=LEAK", nil, RequestOptions{}); err != nil || string(raw) != `{"x":1}` {
		t.Fatal(err)
	}
	r := mock.Requests()[0]
	if r.URL.Path != "/v2/torn/future" || r.URL.Query().Get("cat") != "a" || r.URL.Query().Has("key") || r.Header.Get("Authorization") != "ApiKey LEAK" {
		t.Fatal(r)
	}
}
