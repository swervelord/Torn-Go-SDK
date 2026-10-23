package torn

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/swervelord/Torn-Go-SDK/models"
	"io"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Expectations below come from the official selection enums, endpoint parameters,
// and the documented standalone/direct exceptions in Torn API/01 Combining
// Selections.md. They do not call the SDK generator or planner to build fixtures.
type recordingTransport struct{ calls []*http.Request }

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls = append(r.calls, req)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
}
func plannerTestClient(t *testing.T) (*Client, *recordingTransport) {
	t.Helper()
	r := &recordingTransport{}
	c, e := NewClient(Config{APIKeys: []string{"fixture-only-key"}, HTTPClient: &http.Client{Transport: r}, KeyPerMinute: 1000000, RoutePerMinute: 1000000})
	if e != nil {
		t.Fatal(e)
	}
	return c, r
}
func enumSelections(t *testing.T, version string) map[string][]string {
	t.Helper()
	b, e := os.ReadFile("spec/openapi-" + version + ".json")
	if e != nil {
		t.Fatal(e)
	}
	var doc struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if e = json.Unmarshal(b, &doc); e != nil {
		t.Fatal(e)
	}
	out := map[string][]string{}
	for schema, b := range doc.Components.Schemas {
		if !strings.HasSuffix(schema, "SelectionName") {
			continue
		}
		var v struct {
			OneOf []struct {
				Enum []string `json:"enum"`
			} `json:"oneOf"`
		}
		if json.Unmarshal(b, &v) != nil {
			t.Fatal(schema)
		}
		r := strings.ToLower(strings.TrimSuffix(schema, "SelectionName"))
		for _, v := range v.OneOf {
			out[r] = append(out[r], v.Enum...)
		}
	}
	return out
}

func TestOfficialCurrentAndFrozenSelectionTransport(t *testing.T) {
	scenarios := 0
	for _, version := range []string{"6.13.6", "6.13.8"} {
		for resource, names := range enumSelections(t, version) {
			for _, name := range names {
				if name == "snapshot" {
					continue
				}
				scenarios++
				t.Run(version+"/"+resource+"/"+name, func(t *testing.T) {
					c, r := plannerTestClient(t)
					p, e := PlanSelections(resource, []string{name}, map[string]any{"id": 7, "crimeId": 7, "tradeId": 7, "typeId": 7, "ids": "7", "cat": "test", "unrecognized": "drop"})
					if e != nil {
						t.Fatal(e)
					}
					_, e = c.Execute(context.Background(), p, RequestOptions{})
					if e != nil {
						t.Fatal(e)
					}
					if len(r.calls) != 1 {
						t.Fatalf("want one selection call, got%d", len(r.calls))
					}
					req := r.calls[0]
					if req.URL.Query().Get("unrecognized") != "" {
						t.Fatal("unknown parameter escaped allowlist")
					}
					if !strings.Contains(req.URL.Path, "/"+resource) {
						t.Fatal(req.URL.Path)
					}
					if req.URL.Query().Get("key") != "fixture-only-key" {
						t.Fatal("authentication missing")
					}
				})
			}
		}
	}
	// Controlled documented v1-only compatibility surface, independent of enums.
	for _, resource := range []string{"faction", "torn"} {
		names := []string{"pawnshop"}
		if resource == "faction" {
			names = []string{"armor", "boosters", "caches", "cesium", "drugs", "medical", "temporary", "utilities", "weapons"}
		}
		for _, name := range names {
			scenarios++
			p, e := PlanSelections(resource, []string{name}, map[string]any{"id": 7})
			if e != nil {
				t.Fatal(e)
			}
			if len(p.Calls) != 1 || p.Calls[0].Version != "v1" || p.Calls[0].Operation.Path != "/"+resource+"/7" {
				t.Fatalf("legacy route %s/%s: %+v", resource, name, p)
			}
		}
	}
	t.Logf("%d independently selected current/frozen single-selection routing cases", scenarios)
}

func TestAllDocumentedCompatiblePairsAndGroupsAtTransport(t *testing.T) {
	// This list deliberately omits standalone, CSV, direct-only and selections
	// whose id denotes a different domain. Counts are checked at the HTTP boundary.
	groups := map[string][]string{
		"user":     strings.Fields("ammo attacks attacksfull bars basic battlestats bounties calendar casino competition cooldowns discord enlistedcars equipment events faction factionbalance forumfeed forumfriends forumposts forumsubscribedthreads forumthreads gym hof honors icons inventory itemmarket itemmod job jobpoints jobranks list medals merits messages missions money networth newevents newmessages notifications organizedcrime personalstats profile properties property races racingrecords refills reports revives revivesfull skills stocks trades travel timestamp weaponexp workstats education perks"),
		"faction":  strings.Fields("applications attacks attacksfull balance basic chains crimeexp crimes hof lookup members news positions rackets revives revivesfull stats territory timestamp upgrades wars"),
		"forum":    strings.Fields("categories lookup timestamp"),
		"key":      strings.Fields("info log"),
		"company":  strings.Fields("applications employees lookup news profile stock timestamp"),
		"market":   strings.Fields("bazaar itemmarket timestamp"),
		"racing":   strings.Fields("cars carupgrades tracks timestamp"),
		"property": strings.Fields("lookup timestamp"),
		"torn":     strings.Fields("bounties calendar education honors items itemmods lookup medals properties timestamp"),
	}
	pairs := 0
	for resource, names := range groups {
		sort.Strings(names)
		for i := range names {
			for j := i + 1; j < len(names); j++ {
				pairs++
				t.Run(resource+"/"+names[i]+"+"+names[j], func(t *testing.T) {
					c, r := plannerTestClient(t)
					_, e := c.RawSelections(context.Background(), resource, []string{names[j], names[i], names[i]}, map[string]any{"id": 7}, RequestOptions{})
					if e != nil {
						t.Fatal(e)
					}
					if len(r.calls) != 1 {
						t.Fatalf("compatible pair sent %d requests", len(r.calls))
					}
					q := r.calls[0].URL.Query().Get("selections")
					if q != names[i]+","+names[j] {
						t.Fatalf("selections %q", q)
					}
					if r.calls[0].URL.Path != "/v2/"+resource {
						t.Fatalf("generic path %s", r.calls[0].URL.Path)
					}
				})
			}
		}
		t.Run(resource+"/full-group", func(t *testing.T) {
			c, r := plannerTestClient(t)
			_, e := c.RawSelections(context.Background(), resource, names, map[string]any{"id": 7}, RequestOptions{})
			if e != nil {
				t.Fatal(e)
			}
			if len(r.calls) != 1 {
				t.Fatalf("group: %d calls", len(r.calls))
			}
		})
	}
	if pairs < 466 {
		t.Fatalf("only %d pairs", pairs)
	}
	t.Logf("%d independently enumerated compatible pairs", pairs)
}

func TestPlannerBoundaries(t *testing.T) {
	for _, tt := range []struct {
		r string
		s []string
		p map[string]any
	}{
		{"unknown", []string{"profile"}, nil}, {"user", nil, nil}, {"user", []string{"made-up"}, nil}, {"user", []string{"snapshot"}, nil}, {"user", []string{"trade"}, nil}, {"user", []string{"crimes"}, nil}, {"user", []string{"profile", "trade"}, map[string]any{"id": 7}}, {"user", []string{"profile", "crimes"}, map[string]any{"id": 7}},
	} {
		if _, e := PlanSelections(tt.r, tt.s, tt.p); e == nil {
			t.Errorf("expected rejection %+v", tt)
		}
	}
	p, e := PlanSelections(" USER ", []string{" Profile ", "JOB", "profile", ""}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Calls) != 1 || !reflect.DeepEqual(p.Calls[0].Selections, []string{"job", "profile"}) {
		t.Fatal(p)
	}
	for _, r := range []string{"user", "faction", "company"} {
		p, e := PlanSelections(r, []string{"search", "timestamp"}, map[string]any{"query": "alice"})
		if e != nil {
			t.Fatal(e)
		}
		if len(p.Calls) != 2 {
			t.Fatalf("%s standalone group: %+v", r, p)
		}
	}
	p, e = PlanSelections("user", []string{"trade"}, map[string]any{"tradeId": 123})
	if e != nil {
		t.Fatal(e)
	}
	if fmt.Sprint(p.Calls[0].Params["id"]) != "123" {
		t.Fatal(p)
	}
}

func TestRawConvenienceHelperDispatch(t *testing.T) {
	c, r := plannerTestClient(t)
	typ := reflect.TypeOf(c)
	count := 0
	for i := 0; i < typ.NumMethod(); i++ {
		method := typ.Method(i)
		if !strings.HasPrefix(method.Name, "Raw") || method.Name == "RawSelections" {
			continue
		}
		before := len(r.calls)
		out := reflect.ValueOf(c).Method(i).Call([]reflect.Value{reflect.ValueOf(context.Background()), reflect.ValueOf(map[string]any{"id": 7, "crimeId": 7, "tradeId": 7, "typeId": 7, "ids": "7"}), reflect.ValueOf(RequestOptions{})})
		if !out[1].IsNil() {
			t.Errorf("%s: %v", method.Name, out[1].Interface())
		}
		if len(r.calls) != before+1 {
			t.Errorf("%s sent %d calls", method.Name, len(r.calls)-before)
		}
		count++
	}
	if count != 202 {
		t.Fatalf("raw helper dispatch inventory %d", count)
	}
}

func TestMinimumCallsAcrossRoutingBuckets(t *testing.T) {
	// Lower bounds: one compatible generic bucket, one per direct/standalone
	// endpoint, and one v1 bucket. ID-domain-specific selections are tested
	// separately because a full resource cannot share those target semantics.
	cases := []struct {
		resource, names string
		calls           int
	}{
		{"user", "profile job itemmods log organizedcrimes search virus bazaar criminalrecord display", 7},
		{"faction", "basic members dirtybombs inventory reports search warfare armor boosters caches cesium drugs medical temporary utilities weapons", 7},
		{"company", "applications employees lookup news profile stock timestamp companies search", 3},
		{"forum", "categories lookup timestamp", 1},
		{"key", "info log", 1},
		{"market", "bazaar itemmarket timestamp", 1},
		{"property", "lookup timestamp property", 2},
		{"racing", "cars carupgrades tracks timestamp", 1},
		{"torn", "items honors companies searchforcash shoplifting competition organisedcrimes pawnshop", 5},
	}
	for _, tt := range cases {
		t.Run(tt.resource, func(t *testing.T) {
			c, r := plannerTestClient(t)
			_, e := c.RawSelections(context.Background(), tt.resource, strings.Fields(tt.names), map[string]any{"id": 7, "typeId": 7}, RequestOptions{})
			if e != nil {
				t.Fatal(e)
			}
			if len(r.calls) != tt.calls {
				t.Fatalf("minimum %d calls, got %d", tt.calls, len(r.calls))
			}
		})
	}
	for _, tt := range []struct{ resource, selection, alias string }{{"user", "trade", "tradeId"}, {"user", "crimes", "crimeId"}, {"market", "properties", "propertyTypeId"}} {
		p, e := PlanSelections(tt.resource, []string{tt.selection, "timestamp"}, map[string]any{tt.alias: 7})
		if e != nil {
			t.Fatal(e)
		}
		if len(p.Calls) != 1 || fmt.Sprint(p.Calls[0].Params["id"]) != "7" {
			t.Fatal(p)
		}
	}
}

func TestGeneratedScalarUnionTargets(t *testing.T) {
	id, e := models.NewUserDiscordPathIdVariant1(models.UserId(7))
	if e != nil {
		t.Fatal(e)
	}
	for _, tt := range []struct{ resource, selection, path string }{{"property", "property", "/property/7/property"}, {"user", "bazaar", "/user/7"}} {
		p, e := PlanSelections(tt.resource, []string{tt.selection}, map[string]any{"id": id})
		if e != nil {
			t.Fatal(e)
		}
		if len(p.Calls) != 1 || p.Calls[0].Operation.Path != tt.path {
			t.Fatalf("scalar union route %+v", p)
		}
	}
}

func TestExplicitV1CatalogAndFallbackPolicy(t *testing.T) {
	// This frozen compatibility inventory is intentionally separate from the205
	// current capability entries. Unsupported resources never acquire fallback.
	counts := map[string]int{"user": 66, "faction": 53, "company": 3, "property": 3, "market": 9, "torn": 50}
	catalog := V1Catalog()
	if len(catalog) != len(counts) {
		t.Fatal(catalog)
	}
	for resource, count := range counts {
		if len(catalog[resource]) != count {
			t.Fatalf("%s v1 catalog %d want%d", resource, len(catalog[resource]), count)
		}
		for _, name := range catalog[resource] {
			if !SupportsV1Selection(resource, name) {
				t.Fatal(name)
			}
			p, e := PlanV1Selections(resource, []string{name, name}, map[string]any{"id": 7, "striptags": true, "unsupported": "drop"})
			if e != nil {
				t.Fatal(e)
			}
			if len(p.Calls) != 1 || p.Calls[0].Version != "v1" || p.Calls[0].Params["selections"] != name || p.Calls[0].Params["striptags"] != true {
				t.Fatalf("v1 route %+v", p)
			}
			if _, ok := p.Calls[0].Params["unsupported"]; ok {
				t.Fatal("unsupported filter passed")
			}
		}
	}
	for _, resource := range []string{"forum", "racing", "key"} {
		if SupportsV1Selection(resource, "timestamp") {
			t.Fatal(resource)
		}
		if _, e := PlanV1Selections(resource, []string{"timestamp"}, nil); e == nil {
			t.Fatal(resource)
		}
	}
	for _, tt := range []struct{ resource, selection string }{{"forum", "categories"}, {"racing", "cars"}, {"key", "info"}, {"user", "itemmod"}, {"user", "trade"}, {"user", "factionbalance"}} {
		p, e := PlanSelections(tt.resource, []string{tt.selection}, map[string]any{"id": 7})
		if e != nil {
			t.Fatal(e)
		}
		for _, call := range p.Calls {
			if len(call.FallbackCalls) != 0 {
				t.Fatalf("unsupported fallback %+v", call)
			}
		}
	}
	for _, tt := range []struct{ resource, selection string }{{"user", "profile"}, {"user", "networth"}, {"property", "property"}, {"torn", "companies"}, {"company", "search"}} {
		p, e := PlanSelections(tt.resource, []string{tt.selection}, map[string]any{"id": 7, "typeId": 7})
		if e != nil {
			t.Fatal(e)
		}
		if len(p.Calls) != 1 || len(p.Calls[0].FallbackCalls) != 1 || p.Calls[0].FallbackCalls[0].Version != "v1" {
			t.Fatalf("missing supported fallback %+v", p)
		}
	}
	for _, tt := range []struct{ resource, names string }{{"faction", "armorynews attacknews crimenews currency donations fundsnews mainnews membershipnews territorynews"}, {"torn", "chainreport dirtybombs rackets raidreport raids rankedwarreport rankedwars territorynames territorywarreport territorywars"}} {
		p, e := PlanSelections(tt.resource, strings.Fields(tt.names), nil)
		if e != nil {
			t.Fatal(e)
		}
		if len(p.Calls) != 1 || p.Calls[0].Version != "v1" {
			t.Fatal(p)
		}
	}
	// Public copies must never mutate planner authorization.
	catalog["user"][0] = "invented"
	if SupportsV1Selection("user", "invented") {
		t.Fatal("catalog mutated")
	}
}

func TestCatalogGatesActualFallbackTransport(t *testing.T) {
	for _, tt := range []struct {
		resource, selection string
		supported           bool
	}{{"user", "profile", true}, {"property", "property", true}, {"forum", "categories", false}, {"racing", "cars", false}, {"key", "info", false}, {"user", "trade", false}} {
		t.Run(tt.resource+"/"+tt.selection, func(t *testing.T) {
			var paths []string
			c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if strings.HasPrefix(r.URL.Path, "/v2/") {
					fmt.Fprint(w, `{"error":{"code":4,"error":"unsupported"}}`)
					return
				}
				fmt.Fprint(w, `{}`)
			}, func(cfg *Config) { cfg.BaseURL += "/v2" })
			p, e := PlanSelections(tt.resource, []string{tt.selection}, map[string]any{"id": 7})
			if e != nil {
				t.Fatal(e)
			}
			result, e := c.Execute(context.Background(), p, RequestOptions{AllowLegacyFallback: true})
			if tt.supported {
				if e != nil || len(paths) != 2 || len(result.Report.Calls) != 1 || !result.Report.Calls[0].Fallback {
					t.Fatalf("supported fallback paths=%v result=%+v error=%v", paths, result, e)
				}
			} else if e == nil || len(paths) != 1 || len(result.Report.Calls) != 0 {
				t.Fatalf("unsupported fallback paths=%v result=%+v error=%v", paths, result, e)
			}
		})
	}
}
