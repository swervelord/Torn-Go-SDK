package torn_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	torn "github.com/swervelord/Torn-Go-SDK"
)

// This invokes every facade method through the real transport, including legacy
// custom decoders, mixed-shape endpoints, aliases, and CSV helpers. Response
// acceptance remains covered independently by model/compatibility contract tests.
func TestEveryTypedFacadeReachesTransport(t *testing.T) {
	definitions := loadContract(t, "6.13.8").Components.Schemas
	data, e := os.ReadFile("spec/openapi-6.13.8.json")
	if e != nil {
		t.Fatal(e)
	}
	var official map[string]any
	if e = json.Unmarshal(data, &official); e != nil {
		t.Fatal(e)
	}
	paths := official["paths"].(map[string]any)
	names := map[string]torn.SelectionCapability{}
	for _, cap := range torn.Capabilities() {
		names[facadeName(cap.Resource)+facadeName(cap.Name)] = cap
	}
	names["CompanyDetailed"] = names["CompanyProfile"]
	names["TornStock"] = names["TornStocks"]
	count := 0
	for name, cap := range names {
		count++
		t.Run(name, func(t *testing.T) {
			calls := 0
			response := []byte(`{}`)
			if name == "TornPawnshop" {
				response = []byte(`{"pawnshop":{}}`)
			}
			if name == "UserCriminalrecord" {
				response = []byte(`{"criminalrecord":{}}`)
			}
			if !cap.Legacy && !cap.CSV && len(cap.Endpoints) > 0 {
				ep := cap.Endpoints[0]
				if item, ok := paths[ep.Path].(map[string]any); ok {
					if op, ok := item["get"].(map[string]any); ok {
						schema := op["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
						response, _ = json.Marshal(contractSample(schema, definitions, 1, 0))
					}
				}
			}
			// Official generic-only response associations and the documented
			// singular stock alias select their concrete schema explicitly.
			fixtureSchema := map[string]string{"FactionDirtbombs": "FactionWarfareDirtyBombsResponse", "UserItemmod": "UserItemModsResponse", "UserFactionbalance": "UserFactionBalanceResponse", "TornStock": "TornStockDetailedResponse"}[name]
			if fixtureSchema != "" {
				response, _ = json.Marshal(contractSample(map[string]any{"$ref": "#/components/schemas/" + fixtureSchema}, definitions, 1, 0))
			}
			c, e := torn.NewClient(torn.Config{APIKeys: []string{"fixture-only-key"}, HTTPClient: &http.Client{Transport: operationTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if !strings.Contains(req.URL.Path, "/"+cap.Resource) {
					t.Errorf("wrong resource path %s", req.URL.Path)
				}
				if req.URL.Query().Get("comment") != "facade π" {
					t.Error("comment not encoded")
				}
				media := "application/json"
				body := response
				if cap.CSV {
					media = "text/csv"
					body = []byte("id,name\n7,fixture\n")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(bytes.NewReader(body)), Request: req}, nil
			})}, KeyPerMinute: 1000000, RoutePerMinute: 1000000})
			if e != nil {
				t.Fatal(e)
			}
			method := reflect.ValueOf(c).MethodByName(name)
			if !method.IsValid() {
				t.Fatalf("missing facade %s", name)
			}
			out := method.Call([]reflect.Value{reflect.ValueOf(context.Background()), reflect.ValueOf(map[string]any{"id": 7, "crimeId": 7, "tradeId": 7, "typeId": 7, "ids": "7", "comment": "facade π"}), reflect.ValueOf(torn.RequestOptions{})})
			if len(out) != 3 {
				t.Fatal("invalid typed signature")
			}
			if calls != 1 {
				t.Fatalf("want one transport send, got%d; error=%v", calls, out[2].Interface())
			}
			if cap.CSV && out[0].String() != "id,name\n7,fixture\n" {
				t.Fatal("CSV was not returned intact")
			}
			if !out[2].IsNil() {
				t.Errorf("typed facade decode: %v", out[2].Interface())
			}
		})
	}
	if count != 207 {
		t.Fatal(fmt.Sprintf("typed facade inventory%d", count))
	}
}
func facadeName(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
