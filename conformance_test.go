package torn

// Shared conformance suite (PARITY_SPEC P9.1): every scenario in
// spec/shared/conformance/*.json runs through the mock transport and virtual
// clock. A scenario this harness cannot interpret fails instead of skipping.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/swervelord/Torn-Go-SDK/models"
	"github.com/swervelord/Torn-Go-SDK/torntest"
)

type conformanceFile struct {
	Comment   string                `json:"$comment"`
	Scenarios []conformanceScenario `json:"scenarios"`
}

type conformanceScenario struct {
	ID           string   `json:"id"`
	Requirements []string `json:"requirements"`
	Config       struct {
		Keys            []string               `json:"keys"`
		MaxAttempts     int                    `json:"maxAttempts"`
		PerKeyPerMinute int                    `json:"perKeyPerMinute"`
		Routes          []string               `json:"routes"`
		KeyGroups       [][]string             `json:"keyGroups"`
		CheckAccess     bool                   `json:"checkAccess"`
		KeyAccess       map[string]AccessLevel `json:"keyAccess"`
		Jitter          string                 `json:"jitter"`
	} `json:"config"`
	Clock struct {
		StartMs int64 `json:"startMs"`
	} `json:"clock"`
	Preload []struct {
		Key       string  `json:"key"`
		SendsAtMs []int64 `json:"sendsAtMs"`
	} `json:"preload"`
	Request struct {
		Kind        string         `json:"kind"`
		Resource    string         `json:"resource"`
		Selections  []string       `json:"selections"`
		ID          any            `json:"id"`
		Params      map[string]any `json:"params"`
		OperationID *string        `json:"operationId"`
		Path        *string        `json:"path"`
		MaxPages    *int           `json:"maxPages"`
	} `json:"request"`
	Exchanges []struct {
		Expect struct {
			Method      string            `json:"method"`
			Path        string            `json:"path"`
			Query       map[string]any    `json:"query"`
			AbsentQuery []string          `json:"absentQuery"`
			Auth        string            `json:"auth"`
			AtMs        *int64            `json:"atMs"`
			Headers     map[string]string `json:"headers"`
		} `json:"expect"`
		Respond struct {
			Status       int               `json:"status"`
			Headers      map[string]string `json:"headers"`
			Body         json.RawMessage   `json:"body"`
			NetworkError bool              `json:"networkError"`
		} `json:"respond"`
	} `json:"exchanges"`
	Expect struct {
		Outcome string `json:"outcome"`
		Error   *struct {
			Kind           string      `json:"kind"`
			Code           *int        `json:"code"`
			RequiredAccess AccessLevel `json:"requiredAccess"`
		} `json:"error"`
		Data         map[string]json.RawMessage `json:"data"`
		Sends        int                        `json:"sends"`
		KeyStates    map[string]KeyStatus       `json:"keyStates"`
		MinElapsedMs *int64                     `json:"minElapsedMs"`
		MaxElapsedMs *int64                     `json:"maxElapsedMs"`
		Pages        *int                       `json:"pages"`
		TypedDecode  string                     `json:"typedDecode"`
		UnknownEnum  string                     `json:"unknownEnum"`
		SecretNotIn  string                     `json:"secretNotIn"`
	} `json:"expect"`
}

func loadConformance(t *testing.T) []conformanceScenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("spec", "shared", "conformance", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no conformance files: %v", err)
	}
	sort.Strings(files)
	var all []conformanceScenario
	seen := map[string]bool{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields() // new scenario features must fail loudly
		var doc conformanceFile
		if err := dec.Decode(&doc); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, s := range doc.Scenarios {
			if seen[s.ID] {
				t.Fatalf("duplicate scenario id %s", s.ID)
			}
			seen[s.ID] = true
			all = append(all, s)
		}
	}
	return all
}

func TestSharedConformance(t *testing.T) {
	scenarios := loadConformance(t)
	passed := 0
	for _, sc := range scenarios {
		ok := t.Run(sc.ID, func(t *testing.T) { runConformance(t, sc) })
		if ok {
			passed++
		}
	}
	t.Logf("shared conformance: %d/%d scenarios passed", passed, len(scenarios))
}

func runConformance(t *testing.T, sc conformanceScenario) {
	start := time.UnixMilli(sc.Clock.StartMs)
	clock := torntest.NewClock(start)
	step := 0
	var failures []string
	mock := &torntest.Transport{Clock: clock}
	mock.Handler = func(req *http.Request) torntest.Response {
		if step >= len(sc.Exchanges) {
			failures = append(failures, fmt.Sprintf("unexpected extra send %s %s", req.Method, req.URL.Path))
			step++
			return torntest.JSON(500, `{}`)
		}
		ex := sc.Exchanges[step]
		step++
		e := ex.Expect
		if req.URL.Host != "api.torn.com" {
			failures = append(failures, fmt.Sprintf("send %d host %s", step, req.URL.Host))
		}
		if e.Method != "" && req.Method != e.Method {
			failures = append(failures, fmt.Sprintf("send %d method %s", step, req.Method))
		}
		if e.Path != "" && req.URL.Path != e.Path {
			failures = append(failures, fmt.Sprintf("send %d path %s want %s", step, req.URL.Path, e.Path))
		}
		q := req.URL.Query()
		for k, v := range e.Query {
			if got := q.Get(k); got != wireString(v) {
				failures = append(failures, fmt.Sprintf("send %d query %s=%q want %q", step, k, got, wireString(v)))
			}
		}
		for _, k := range e.AbsentQuery {
			if q.Has(k) {
				failures = append(failures, fmt.Sprintf("send %d has forbidden query %s", step, k))
			}
		}
		if e.Auth != "" {
			// P2.1: header-only auth on v1 and v2.
			if req.Header.Get("Authorization") != "ApiKey "+e.Auth || q.Has("key") {
				failures = append(failures, fmt.Sprintf("send %d auth %q/%q want %s", step, req.Header.Get("Authorization"), q.Get("key"), e.Auth))
			}
		}
		for k, v := range e.Headers {
			if req.Header.Get(k) != v {
				failures = append(failures, fmt.Sprintf("send %d header %s", step, k))
			}
		}
		if e.AtMs != nil && clock.Now().Before(time.UnixMilli(*e.AtMs)) {
			failures = append(failures, fmt.Sprintf("send %d at %dms, want >= %dms", step, clock.Now().UnixMilli(), *e.AtMs))
		}
		r := ex.Respond
		if r.NetworkError {
			return torntest.NetworkError()
		}
		var body []byte
		var text string
		if json.Unmarshal(r.Body, &text) == nil {
			body = []byte(text)
		} else {
			body = r.Body
		}
		resp := torntest.JSON(r.Status, body)
		for k, v := range r.Headers {
			resp.Header.Set(k, v)
		}
		return resp
	}
	cfg := Config{
		APIKeys:      sc.Config.Keys,
		HTTPClient:   &http.Client{Transport: mock},
		Scheduler:    clock,
		Attempts:     sc.Config.MaxAttempts,
		KeyPerMinute: sc.Config.PerKeyPerMinute,
		KeyGroups:    sc.Config.KeyGroups,
		CheckAccess:  sc.Config.CheckAccess,
		KeyAccess:    sc.Config.KeyAccess,
		Jitter:       JitterMode(sc.Config.Jitter),
	}
	for _, r := range sc.Config.Routes {
		cfg.Routes = append(cfg.Routes, Route{Name: r})
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range sc.Preload {
		idx := c.pool.indexOf(p.Key)
		if idx < 0 {
			t.Fatalf("preload names unknown key")
		}
		for _, ms := range p.SendsAtMs {
			c.recordUsage(idx, time.UnixMilli(ms))
		}
	}
	ctx := context.Background()
	req := sc.Request
	params := map[string]any{}
	for k, v := range req.Params {
		params[k] = v
	}
	if req.ID != nil {
		params["id"] = req.ID
	}
	var data json.RawMessage
	var callErr error
	pages := 0
	switch req.Kind {
	case "selections":
		var res ExecutionResult
		res, callErr = c.RawSelections(ctx, req.Resource, req.Selections, params, RequestOptions{})
		data = res.Data
	case "operation":
		if req.OperationID == nil {
			t.Fatal("operation scenario without operationId")
		}
		m, ok := MetadataForOperation(*req.OperationID)
		if !ok {
			t.Fatalf("unknown operation %s", *req.OperationID)
		}
		data, _, callErr = c.DoOperation(ctx, Operation{ID: *req.OperationID, Path: m.Path, Method: "GET"}, params, RequestOptions{})
	case "raw":
		if req.Path == nil {
			t.Fatal("raw scenario without path")
		}
		data, _, callErr = c.DoOperation(ctx, Operation{ID: "raw", Path: *req.Path, Method: "GET"}, params, RequestOptions{})
	case "paginate":
		if req.Path == nil {
			t.Fatal("paginate scenario without path")
		}
		opts := PageOptions{}
		if req.MaxPages != nil {
			opts.MaxPages = *req.MaxPages
		}
		for page, err := range c.Pages(*req.Path, params, opts).All(ctx) {
			if err != nil {
				callErr = err
				break
			}
			pages++
			data = page.Raw
		}
	default:
		t.Fatalf("unsupported request kind %q", req.Kind)
	}
	for _, f := range failures {
		t.Error(f)
	}
	ex := sc.Expect
	if step != len(sc.Exchanges) || step != ex.Sends {
		t.Errorf("sends %d, exchanges %d, expected %d", step, len(sc.Exchanges), ex.Sends)
	}
	switch ex.Outcome {
	case "ok":
		if callErr != nil {
			t.Fatalf("unexpected error: %v", callErr)
		}
	case "error":
		if callErr == nil {
			t.Fatal("expected an error")
		}
		if ex.Error != nil {
			if kind := errorKind(callErr); kind != ex.Error.Kind {
				t.Errorf("error kind %s want %s (%v)", kind, ex.Error.Kind, callErr)
			}
			if ex.Error.Code != nil {
				if code, ok := Code(callErr); !ok || int(code) != *ex.Error.Code {
					t.Errorf("error code %v want %d", code, *ex.Error.Code)
				}
			}
			if ex.Error.RequiredAccess != "" {
				var a *AccessError
				if !errors.As(callErr, &a) || a.Required != ex.Error.RequiredAccess {
					t.Errorf("access error %v want required %s", callErr, ex.Error.RequiredAccess)
				}
			}
		}
	default:
		t.Fatalf("unsupported outcome %q", ex.Outcome)
	}
	for path, want := range ex.Data {
		got, ok := jsonPath(data, path)
		if !ok || !jsonEqual(got, want) {
			t.Errorf("data %s = %s want %s", path, got, want)
		}
	}
	for key, want := range ex.KeyStates {
		idx := c.pool.indexOf(key)
		if idx < 0 {
			t.Fatalf("keyStates names unknown key")
		}
		if got := c.KeyStates()[idx].Status; got != want {
			t.Errorf("key %d state %s want %s", idx, got, want)
		}
	}
	elapsed := clock.Now().Sub(start).Milliseconds()
	if ex.MinElapsedMs != nil && elapsed < *ex.MinElapsedMs {
		t.Errorf("elapsed %dms < %d", elapsed, *ex.MinElapsedMs)
	}
	if ex.MaxElapsedMs != nil && elapsed > *ex.MaxElapsedMs {
		t.Errorf("elapsed %dms > %d", elapsed, *ex.MaxElapsedMs)
	}
	if ex.Pages != nil && pages != *ex.Pages {
		t.Errorf("pages %d want %d", pages, *ex.Pages)
	}
	if ex.TypedDecode != "" || ex.UnknownEnum != "" {
		value, decodeErr := conformanceTypedDecode(req.Resource, req.Selections, data)
		if (decodeErr == nil) != (ex.TypedDecode != "error") {
			t.Errorf("typed decode %v, want %s", decodeErr, ex.TypedDecode)
		}
		if ex.UnknownEnum != "" {
			checkUnknownEnum(t, value, data, ex.UnknownEnum)
		}
	}
	if ex.SecretNotIn != "" {
		for _, s := range formatAll(callErr, cfg, c, c.KeyStates()) {
			if strings.Contains(s, ex.SecretNotIn) {
				t.Errorf("secret leaked in %q", s)
			}
		}
	}
}

func wireString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		parts := make([]string, len(x))
		for i, p := range x {
			parts[i] = wireString(p)
		}
		return strings.Join(parts, ",")
	case float64:
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%f", x), "0"), ".")
	}
	return fmt.Sprint(v)
}

func errorKind(err error) string {
	var none *NoUsableKeysError
	var api *APIError
	var h *HTTPError
	var tr *TransportError
	var d *DecodeError
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) && !errors.As(err, &tr):
		return "cancelled"
	case errors.As(err, &none):
		return "no_usable_keys"
	case errors.Is(err, ErrValidation):
		return "validation"
	case errors.As(err, &d):
		return "decode"
	case errors.As(err, &api):
		return "api"
	case errors.As(err, &h):
		return "http"
	case errors.As(err, &tr):
		return "transport"
	}
	return "unknown"
}

func jsonPath(data json.RawMessage, path string) (json.RawMessage, bool) {
	cur := data
	for _, part := range strings.Split(path, ".") {
		var m map[string]json.RawMessage
		if json.Unmarshal(cur, &m) != nil {
			return nil, false
		}
		next, ok := m[part]
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// conformanceTypedDecode decodes the result into the selection's generated model.
func conformanceTypedDecode(resource string, selections []string, data json.RawMessage) (any, error) {
	if len(selections) != 1 {
		return nil, fmt.Errorf("typed decode needs exactly one selection")
	}
	for _, c := range Capabilities() {
		if c.Resource == resource && c.Name == selections[0] && len(c.Endpoints) > 0 {
			return models.DecodeSchema(c.Endpoints[0].Schema, data)
		}
	}
	return nil, fmt.Errorf("no generated model for %s/%s", resource, selections[0])
}

// checkUnknownEnum walks a dotted JSON path through the decoded model by JSON
// field names and asserts the enum there is unknown with its raw value kept.
func checkUnknownEnum(t *testing.T, value any, data json.RawMessage, path string) {
	t.Helper()
	v := reflect.ValueOf(value)
	for _, part := range strings.Split(path, ".") {
		for v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			t.Fatalf("unknownEnum path %s: %s is not an object", path, part)
		}
		found := false
		for i := 0; i < v.NumField(); i++ {
			if strings.Split(v.Type().Field(i).Tag.Get("json"), ",")[0] == part {
				v = v.Field(i)
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("unknownEnum path %s: no field %s", path, part)
		}
		if strings.HasPrefix(v.Type().Name(), "Optional[") {
			v = v.FieldByName("Value")
		}
	}
	known := v.MethodByName("IsKnown")
	if !known.IsValid() {
		t.Fatalf("unknownEnum path %s: %s has no IsKnown", path, v.Type())
	}
	if known.Call(nil)[0].Bool() {
		t.Errorf("unknownEnum path %s decoded as known", path)
	}
	raw, _ := jsonPath(data, path)
	var want string
	_ = json.Unmarshal(raw, &want)
	if v.String() != want {
		t.Errorf("unknownEnum raw %q want %q", v.String(), want)
	}
}

// formatAll renders values through every public formatting path (P2.2).
func formatAll(values ...any) []string {
	var out []string
	for _, v := range values {
		if v == nil {
			continue
		}
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			out = append(out, fmt.Sprintf(verb, v))
		}
		if b, err := json.Marshal(v); err == nil {
			out = append(out, string(b))
		}
		if e, ok := v.(error); ok {
			out = append(out, e.Error())
			for u := errors.Unwrap(e); u != nil; u = errors.Unwrap(u) {
				out = append(out, u.Error(), fmt.Sprintf("%#v", u))
			}
		}
	}
	return out
}
