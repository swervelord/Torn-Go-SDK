package torn_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	torn "github.com/swervelord/Torn-Go-SDK"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type operationTransport func(*http.Request) (*http.Response, error)

func (f operationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Every expected path, wire parameter, response schema and CSV media type is read
// independently from the committed official specification, never from Operations
// or generated metadata. The public typed method itself is invoked reflectively.
func TestEveryExactOperationAgainstOfficialTransportContract(t *testing.T) {
	data, e := os.ReadFile("spec/openapi-6.13.8.json")
	if e != nil {
		t.Fatal(e)
	}
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if e = dec.Decode(&doc); e != nil {
		t.Fatal(e)
	}
	components := doc["components"].(map[string]any)
	definitions := loadContract(t, "6.13.8").Components.Schemas
	paths := doc["paths"].(map[string]any)
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)
	checked, csvCount := 0, 0
	for _, path := range ordered {
		item := paths[path].(map[string]any)
		for httpMethod, operation := range item {
			op, ok := operation.(map[string]any)
			if !ok {
				continue
			}
			id, ok := op["operationId"].(string)
			if !ok {
				continue
			}
			checked++
			t.Run(id, func(t *testing.T) {
				args := map[string]any{}
				expectedQuery := url.Values{}
				expectedPath := path
				parameters := []any{}
				if a, ok := item["parameters"].([]any); ok {
					parameters = append(parameters, a...)
				}
				if a, ok := op["parameters"].([]any); ok {
					parameters = append(parameters, a...)
				}
				for _, v := range parameters {
					p := v.(map[string]any)
					if ref, ok := p["$ref"].(string); ok {
						p = components["parameters"].(map[string]any)[strings.TrimPrefix(ref, "#/components/parameters/")].(map[string]any)
					}
					key := p["name"].(string)
					if key == "key" {
						continue
					}
					value := contractSample(p["schema"].(map[string]any), definitions, 1, 0)
					if key == "comment" {
						value = "dispatch & unicode π"
					}
					args[key] = value
					wire := contractWire(value)
					if p["in"] == "path" {
						expectedPath = strings.ReplaceAll(expectedPath, "{"+key+"}", url.PathEscape(wire))
					} else if p["in"] == "query" {
						expectedQuery.Set(key, wire)
					}
				}
				content := op["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)
				media := "application/json"
				csv := content["text/csv"] != nil
				if csv {
					media = "text/csv"
					csvCount++
				}
				schema := content[media].(map[string]any)["schema"].(map[string]any)
				var response []byte
				if csv {
					response = []byte("id,name\n1,fixture\n")
				} else {
					response, e = json.Marshal(contractSample(schema, definitions, 1, 0))
					if e != nil {
						t.Fatal(e)
					}
				}
				calls := 0
				transport := operationTransport(func(req *http.Request) (*http.Response, error) {
					calls++
					if req.Method != strings.ToUpper(httpMethod) {
						t.Errorf("method %s", req.Method)
					}
					if req.URL.EscapedPath() != expectedPath {
						t.Errorf("path got %q want %q", req.URL.EscapedPath(), expectedPath)
					}
					q := req.URL.Query()
					q.Del("key")
					if !reflect.DeepEqual(q, expectedQuery) {
						t.Errorf("query got %v want %v", q, expectedQuery)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {media}}, Body: io.NopCloser(bytes.NewReader(response)), Request: req}, nil
				})
				client, e := torn.NewClient(torn.Config{APIKeys: []string{"dispatch-fixture"}, BaseURL: "https://fixture.test", Routes: []torn.Route{{Name: "verification", Transport: transport}}, KeyPerMinute: 10000, RoutePerMinute: 10000})
				if e != nil {
					t.Fatal(e)
				}
				methodName := strings.ToUpper(id[:1]) + id[1:]
				method := reflect.ValueOf(client).MethodByName(methodName)
				if !method.IsValid() {
					t.Fatalf("missing public exact method %s", methodName)
				}
				params := reflect.New(method.Type().In(1))
				encoded, _ := json.Marshal(args)
				if e = json.Unmarshal(encoded, params.Interface()); e != nil {
					t.Fatalf("parameters decode: %v (%s)", e, encoded)
				}
				opts := torn.RequestOptions{Route: "verification", Attempts: 1, Timeout: time.Second}
				results := method.CallSlice([]reflect.Value{reflect.ValueOf(context.Background()), params.Elem(), reflect.ValueOf([]torn.RequestOptions{opts})})
				if !results[2].IsNil() {
					t.Fatalf("typed dispatch failed: %v", results[2].Interface())
				}
				report := results[1].Interface().(torn.ExecutionReport)
				if calls != 1 || len(report.Calls) != 1 {
					t.Fatalf("calls=%d report=%+v", calls, report)
				}
				if report.Calls[0].OperationID != id || report.Calls[0].Route != "verification" {
					t.Fatalf("report %+v", report)
				}
				if csv && results[0].String() != string(response) {
					t.Fatalf("CSV mismatch")
				}
			})
		}
	}
	if checked != 234 || csvCount != 3 {
		t.Fatalf("operation inventory %d CSV %d", checked, csvCount)
	}
}
func contractWire(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return string(x)
	case []any:
		parts := make([]string, len(x))
		for i, a := range x {
			parts[i] = contractWire(a)
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(v)
	}
}
