package torn_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	torn "github.com/swervelord/Torn-Go-SDK"
	"github.com/swervelord/Torn-Go-SDK/models"
)

// Fixtures in testdata/6.15.7 are scrubbed live 6.15.7 payloads (ids and names
// anonymised, no credential) except faction_reports_stockanalysis.json, which is
// built from the official schema because the test key's faction has no reports.
func fixture6157(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/6.15.7/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// assertRoundTrip checks that re-encoding a decoded model keeps the payload.
func assertRoundTrip(t *testing.T, payload []byte, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal(payload, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round trip changed payload: %s", encoded)
	}
}

func TestCurrentOperationMetadataMatchesOfficialContract(t *testing.T) {
	data, err := os.ReadFile("spec/openapi-6.15.7.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			ID         string `json:"operationId"`
			Stability  string `json:"x-stability"`
			Parameters []struct {
				Ref string `json:"$ref"`
			}
		}
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	// P6.1: access levels come from the vendored spec/shared/access-levels.json
	// and must agree with the OpenAPI key parameters.
	var shared struct {
		SpecVersion string `json:"spec_version"`
		Operations  map[string]struct{ Access string }
	}
	sharedData, err := os.ReadFile("spec/shared/access-levels.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(sharedData, &shared); err != nil {
		t.Fatal(err)
	}
	if len(shared.Operations) != 237 || shared.SpecVersion != "6.15.7" {
		t.Fatalf("shared access levels: %d operations, spec %q", len(shared.Operations), shared.SpecVersion)
	}
	count := 0
	levels := map[string]int{}
	for _, methods := range doc.Paths {
		for _, op := range methods {
			if op.ID == "" {
				continue
			}
			count++
			access := ""
			for _, p := range op.Parameters {
				if name := strings.TrimPrefix(p.Ref, "#/components/parameters/ApiKey"); name != p.Ref {
					access = strings.ToLower(name)
				}
			}
			levels[access]++
			got, ok := torn.MetadataForOperation(op.ID)
			if !ok || got.Stability != op.Stability || string(got.AccessLevel) != access || shared.Operations[op.ID].Access != access {
				t.Errorf("%s metadata: %#v, %v; want %q / %q", op.ID, got, ok, op.Stability, access)
			}
		}
	}
	if count != 237 || len(torn.Operations) != 237 {
		t.Fatalf("metadata inventory: %d spec, %d SDK", count, len(torn.Operations))
	}
	if levels[""] != 0 || levels["public"] == 0 || levels["minimal"] == 0 || levels["limited"] == 0 || levels["full"] == 0 {
		t.Fatalf("access level inventory: %v", levels)
	}
	for id, want := range map[string]torn.OperationMetadata{
		"getMyGym":            {Stability: "Stable", AccessLevel: torn.AccessPublic, ResponseSchema: "UserGymResponse", Path: "/user/gym"},
		"getTornCalendar":     {Stability: "Stable", AccessLevel: torn.AccessPublic, ResponseSchema: "TornCalendarResponse", Path: "/torn/calendar"},
		"getRacingRaces":      {Stability: "Stable", AccessLevel: torn.AccessPublic, Paginated: true, ResponseSchema: "RacingRacesResponse", Path: "/racing/races"},
		"getMyLogs":           {Stability: "Stable", AccessLevel: torn.AccessFull, Paginated: true, ResponseSchema: "UserLogsResponse", Path: "/user/log"},
		"getMyFactionReports": {Stability: "Stable", AccessLevel: torn.AccessLimited, Paginated: true, ResponseSchema: "ReportsResponse", Path: "/faction/reports"},
	} {
		got, ok := torn.MetadataForOperation(id)
		want.Stability = got.Stability // stability is checked against the spec above
		if !ok || got != want {
			t.Errorf("%s metadata: %#v; want %#v", id, got, want)
		}
	}
	if torn.APIVersion != "6.15.7" || torn.SpecVersion != "6.15.7" {
		t.Fatalf("APIVersion %q", torn.APIVersion)
	}
}

func TestUserGymUnlockedGymsLiveSample(t *testing.T) {
	payload := fixture6157(t, "user_gym")
	if err := models.ValidateStrict("UserGymResponse", payload); err != nil {
		t.Fatal(err)
	}
	var response models.UserGymResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	gym := response.Gym
	if gym.ID != 26 || gym.Name != "Frontline Fitness" || len(gym.UnlockedGyms) != 4 {
		t.Fatalf("gym shape: %#v", gym)
	}
	first, last := gym.UnlockedGyms[0], gym.UnlockedGyms[3]
	if first.ID != 1 || first.Name != "Premier Fitness" || !first.Available || last.ID != 25 || last.Name != "Balboas Gym" || last.Available {
		t.Fatalf("unlocked gyms: %#v %#v", first, last)
	}
	assertRoundTrip(t, payload, response)
	for _, invalid := range []string{
		`{"gym":{"id":1,"name":"x"}}`,
		`{"gym":{"id":1,"name":"x","unlocked_gyms":{}}}`,
		`{"gym":{"id":1,"name":"x","unlocked_gyms":[{"id":1,"name":"x"}]}}`,
		`{"gym":{"id":1,"name":"x","unlocked_gyms":[{"id":1,"name":"x","available":"yes"}]}}`,
		`{"gym":{"id":1,"name":"x","unlocked_gyms":[{"id":"1","name":"x","available":true}]}}`,
	} {
		var out models.UserGymResponse
		if err := json.Unmarshal([]byte(invalid), &out); err == nil {
			t.Errorf("accepted malformed gym: %s", invalid)
		}
		if err := models.Validate("UserGymResponse", []byte(invalid)); err == nil {
			t.Errorf("validated malformed gym: %s", invalid)
		}
	}
}

func TestTornCalendarActivityWindowLiveSample(t *testing.T) {
	payload := fixture6157(t, "torn_calendar")
	if err := models.ValidateStrict("TornCalendarResponse", payload); err != nil {
		t.Fatal(err)
	}
	var response models.TornCalendarResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	calendar := response.Calendar
	if len(calendar.Competitions) != 1 || len(calendar.Events) != 20 {
		t.Fatalf("calendar shape: %d competitions, %d events", len(calendar.Competitions), len(calendar.Events))
	}
	fixed := calendar.Competitions[0]
	if fixed.Title != "Elimination" || !fixed.FixedStartTime || fixed.EarliestStart != fixed.Start || fixed.LatestEnd != fixed.End || fixed.Start != 1788609600 {
		t.Fatalf("fixed competition: %#v", fixed)
	}
	personal := calendar.Events[2]
	if personal.Title != "Valentine's Day" || personal.FixedStartTime || personal.EarliestStart != 1770976800 || personal.LatestEnd != 1771171199 || personal.EarliestStart >= personal.Start || personal.LatestEnd <= personal.End {
		t.Fatalf("personal-time event: %#v", personal)
	}
	assertRoundTrip(t, payload, response)
	valid := `{"title":"t","description":"d","start":1,"end":2,"earliest_start":0,"latest_end":3,"fixed_start_time":false}`
	var activity models.TornCalendarActivity
	if err := json.Unmarshal([]byte(valid), &activity); err != nil || activity.LatestEnd != 3 {
		t.Fatalf("minimal activity: %#v %v", activity, err)
	}
	for _, invalid := range []string{
		`{"title":"t","description":"d","start":1,"end":2,"latest_end":3,"fixed_start_time":false}`,
		`{"title":"t","description":"d","start":1,"end":2,"earliest_start":0,"fixed_start_time":false}`,
		`{"title":"t","description":"d","start":1,"end":2,"earliest_start":0,"latest_end":3}`,
		`{"title":"t","description":"d","start":1,"end":2,"earliest_start":"0","latest_end":3,"fixed_start_time":false}`,
		`{"title":"t","description":"d","start":1,"end":2,"earliest_start":0,"latest_end":3,"fixed_start_time":"false"}`,
	} {
		if err := json.Unmarshal([]byte(invalid), &activity); err == nil {
			t.Errorf("accepted malformed activity: %s", invalid)
		}
	}
}

func TestAttackLogParticipantsLiveSample(t *testing.T) {
	payload := fixture6157(t, "torn_attacklog")
	if err := models.ValidateStrict("AttackLogResponse", payload); err != nil {
		t.Fatal(err)
	}
	var response models.AttackLogResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	log := response.Attacklog.Log
	if len(log) != 7 || len(response.Attacklog.Summary) != 5 {
		t.Fatalf("attack log shape: %d entries", len(log))
	}
	hit := log[3]
	attacker, err := hit.Attacker.AsVariant1()
	if err != nil {
		t.Fatal(err)
	}
	item, err := attacker.Item.AsVariant1()
	if err != nil || attacker.ID != 1001 || attacker.Name != "PlayerA" || item.ID != 177 || item.Name != "Cobra Derringer" {
		t.Fatalf("attacker: %#v %#v %v", attacker, item, err)
	}
	defender, err := hit.Defender.AsVariant1()
	if err != nil || defender.ID != 1002 || defender.Name != "PlayerB" {
		t.Fatalf("defender: %#v %v", defender, err)
	}
	if _, err := hit.Attacker.AsVariant2(); err == nil {
		t.Fatal("concrete attacker accepted as null")
	}
	if hit.AttackerItem.Present {
		t.Fatal("attacker_item is only sent for stealthed attackers")
	}
	stealthed := log[6]
	if _, err := stealthed.Attacker.AsVariant2(); err != nil || string(stealthed.Attacker.RawJSON()) != "null" {
		t.Fatalf("stealthed attacker: %s %v", stealthed.Attacker.RawJSON(), err)
	}
	if _, err := stealthed.Attacker.AsVariant1(); err == nil {
		t.Fatal("null attacker accepted as AttackLogAttacker")
	}
	if _, err := stealthed.Defender.AsVariant2(); err != nil {
		t.Fatalf("stealthed defender: %v", err)
	}
	if !stealthed.AttackerItem.Present || !stealthed.AttackerItem.Null {
		t.Fatalf("stealthed attacker_item: %#v", stealthed.AttackerItem)
	}
	assertRoundTrip(t, payload, response)

	var resolved models.AttackLog
	if err := json.Unmarshal([]byte(`{"text":"x","timestamp":1,"action":"hit","icon":"i","attacker":null,"defender":null,"attacker_item":{"id":814,"name":"Tyrosine"}}`), &resolved); err != nil {
		t.Fatal(err)
	}
	if got, err := resolved.AttackerItem.Value.AsVariant1(); err != nil || got.ID != 814 || got.Name != "Tyrosine" {
		t.Fatalf("resolved attacker_item: %#v %v", got, err)
	}
	for _, invalid := range []string{
		`{"text":"x","timestamp":1,"action":"hit","icon":"i","defender":null}`,
		`{"text":"x","timestamp":1,"action":"hit","icon":"i","attacker":null}`,
		`{"text":"x","timestamp":1,"action":"hit","icon":"i","attacker":{"id":1,"name":"a"},"defender":null}`,
		`{"text":"x","timestamp":1,"action":"hit","icon":"i","attacker":null,"defender":null,"attacker_item":{"id":1}}`,
		`{"text":"x","timestamp":1,"action":"hit","icon":"i","attacker":null,"defender":{"id":"1","name":"d"}}`,
	} {
		var out models.AttackLog
		if err := json.Unmarshal([]byte(invalid), &out); err == nil {
			t.Errorf("accepted malformed attack log entry: %s", invalid)
		}
	}
	// The pre-6.15.7 inline names remain as deprecated aliases.
	var legacyAttacker models.AttackLogAttackerVariant1 = attacker
	var legacyItem models.AttackLogAttackerVariant1ItemVariant1 = item
	var legacyDefender models.AttackLogDefenderVariant1 = defender
	wrapped, err := models.NewAttackLogAttackerVariant1ItemVariant1(legacyItem)
	if err != nil || legacyAttacker.ID != 1001 || legacyDefender.ID != 1002 {
		t.Fatalf("deprecated aliases: %v", err)
	}
	if back, err := wrapped.AsVariant1(); err != nil || back.ID != item.ID || back.Name != item.Name {
		t.Fatalf("deprecated item constructor: %#v %v", back, err)
	}
}

func TestStockAnalysisRestockTimestamp(t *testing.T) {
	payload := fixture6157(t, "faction_reports_stockanalysis")
	if err := models.ValidateStrict("ReportsResponse", payload); err != nil {
		t.Fatal(err)
	}
	var response models.ReportsResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Reports) != 1 || response.Reports[0].Type != models.ReportTypeEnumStockanalysis {
		t.Fatalf("reports: %#v", response.Reports)
	}
	analysis, err := response.Reports[0].Report.AsVariant8()
	if err != nil || len(analysis.Items) != 2 {
		t.Fatalf("stock analysis: %#v %v", analysis, err)
	}
	pending, restocked := analysis.Items[0], analysis.Items[1]
	if _, err := pending.RestockTimestamp.AsVariant2(); err != nil || string(pending.RestockTimestamp.RawJSON()) != "null" {
		t.Fatalf("null restock_timestamp: %s %v", pending.RestockTimestamp.RawJSON(), err)
	}
	if _, err := pending.RestockTimestamp.AsVariant1(); err == nil {
		t.Fatal("null restock_timestamp accepted as integer")
	}
	if ts, err := restocked.RestockTimestamp.AsVariant1(); err != nil || ts != 1794231200 {
		t.Fatalf("restock_timestamp %d: %v", ts, err)
	}
	if pending.Item.Stock != 1520 || restocked.Item.Stock != 0 || pending.Country != models.CountryEnumMexico {
		t.Fatalf("typed stock: %#v %#v", pending.Item, restocked.Item)
	}
	assertRoundTrip(t, payload, response)
	item := `"item":{"id":1,"name":"x","stock":1,"price":1,"value":1,"due":null}`
	var row models.ReportStockAnalysisItemsItem
	for _, invalid := range []string{
		`{"country":"Mexico",` + item + `,"trip_duration":1,"hourly_profit":1}`,
		`{"country":"Mexico",` + item + `,"trip_duration":1,"hourly_profit":1,"restock_timestamp":"1"}`,
		`{"country":"Mexico","item":{"id":1,"name":"x","stock":"1","price":1,"value":1,"due":null},"trip_duration":1,"hourly_profit":1,"restock_timestamp":null}`,
	} {
		if err := json.Unmarshal([]byte(invalid), &row); err == nil {
			t.Errorf("accepted malformed stock analysis row: %s", invalid)
		}
	}
}

func TestRacingRacesCategoryEnum(t *testing.T) {
	if got := models.RacingRacesCategoryEnumValues(); !reflect.DeepEqual(got, []models.RacingRacesCategoryEnum{models.RacingRacesCategoryEnumOfficial, models.RacingRacesCategoryEnumCustom, models.RacingRacesCategoryEnumAvailable}) {
		t.Fatalf("values: %v", got)
	}
	for _, value := range []string{"official", "custom", "available"} {
		var cat models.RacingRacesCategoryEnum
		if err := json.Unmarshal([]byte(`"`+value+`"`), &cat); err != nil || string(cat) != value || !cat.IsKnown() {
			t.Fatalf("%s: %q %v", value, cat, err)
		}
		if err := models.ValidateStrict("RacingRacesCategoryEnum", []byte(`"`+value+`"`)); err != nil {
			t.Fatalf("strict %s: %v", value, err)
		}
	}
	// P1.1: near misses decode as unknown values and fail strict validation.
	var cat models.RacingRacesCategoryEnum
	if err := json.Unmarshal([]byte(`"Available"`), &cat); err != nil || cat.IsKnown() {
		t.Fatalf("near miss: %q %v", cat, err)
	}
	if err := models.ValidateStrict("RacingRacesCategoryEnum", []byte(`"Available"`)); err == nil {
		t.Fatal("strict validation accepted a near miss")
	}
	// RacingRaceTypeEnum (user races, selection categories) does not gain the value.
	if models.RacingRaceTypeEnum("available").IsKnown() {
		t.Fatal("available must stay a racing/races-only category")
	}
	generic, err := models.NewGetRacingGenericParamCatVariant1(models.RacingRacesCategoryEnumAvailable)
	if err != nil || string(generic.RawJSON()) != `"available"` {
		t.Fatalf("generic racing cat: %s %v", generic.RawJSON(), err)
	}
}

func TestRacingRacesAvailableRequestAndDecode(t *testing.T) {
	payload := fixture6157(t, "racing_races_available")
	if err := models.ValidateStrict("RacingRacesResponse", payload); err != nil {
		t.Fatal(err)
	}
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path+"?"+r.URL.RawQuery)
		if r.URL.Query().Get("key") != "" || r.Header.Get("Authorization") != "ApiKey mock-only" {
			t.Errorf("key must travel only in the Authorization header")
		}
		w.Write(payload)
	}))
	defer srv.Close()
	c, err := torn.NewClient(torn.Config{APIKeys: []string{"mock-only"}, BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	available := models.RacingRacesCategoryEnumAvailable
	exact, _, err := c.GetRacingRaces(ctx, torn.GetRacingRacesParams{Cat: &available})
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0] != "/racing/races?cat=available" {
		t.Fatalf("exact request: %v", requests)
	}
	if len(exact.Races) != 3 {
		t.Fatalf("races: %d", len(exact.Races))
	}
	for _, race := range exact.Races {
		if _, err := race.Schedule.End.AsVariant2(); err != nil || race.Status != models.RaceStatusEnumOpen || race.Schedule.Start <= race.Schedule.JoinFrom {
			t.Fatalf("not-yet-started race %d: %#v %v", race.ID, race.Schedule, err)
		}
	}
	if car, err := exact.Races[1].Requirements.CarItemId.AsVariant1(); err != nil || car != 91 {
		t.Fatalf("car requirement: %d %v", car, err)
	}
	assertRoundTrip(t, payload, exact)

	plan, err := torn.PlanSelections("racing", []string{"races"}, map[string]any{"cat": "available"})
	if err != nil || len(plan.Calls) != 1 || plan.Calls[0].Params["cat"] != "available" {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	helper, _, err := c.RacingRaces(ctx, map[string]any{"cat": "available"}, torn.RequestOptions{})
	if err != nil || len(helper.Races) != 3 {
		t.Fatalf("typed helper: %v", err)
	}
	if last := requests[len(requests)-1]; !strings.Contains(last, "cat=available") {
		t.Fatalf("typed helper request: %s", last)
	}
}

func TestMultiIDPathParametersStayCommaJoined(t *testing.T) {
	doc := loadContract(t, "6.15.7")
	sample := func(schema string) []byte {
		raw, _ := json.Marshal(contractSample(doc.Components.Schemas[schema], doc.Components.Schemas, 0, 0))
		return raw
	}
	bodies := map[string][]byte{
		"/torn/1,2/items":     sample("TornItemsResponse"),
		"/torn/3,4/medals":    sample("TornMedalsResponse"),
		"/forum/5,6/threads":  sample("ForumThreadsResponse"),
		"/torn/7,8/itemstats": sample("TornItemStatsResponse"),
	}
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		body, ok := bodies[r.URL.EscapedPath()]
		if !ok {
			t.Errorf("unexpected path %s", r.URL.EscapedPath())
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	c, err := torn.NewClient(torn.Config{APIKeys: []string{"mock-only"}, BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, _, err := c.GetTornItemsSpecific(ctx, torn.GetTornItemsSpecificParams{Ids: []models.ItemId{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.GetTornMedalsSpecific(ctx, torn.GetTornMedalsSpecificParams{Ids: []models.MedalId{3, 4}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.GetForumThreads(ctx, torn.GetForumThreadsParams{CategoryIds: []models.ForumId{5, 6}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.GetTornItemStats(ctx, torn.GetTornItemStatsParams{Ids: []models.ItemUid{7, 8}}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"/torn/1,2/items", "/torn/3,4/medals", "/forum/5,6/threads", "/torn/7,8/itemstats"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths %v; want %v", paths, want)
	}
}

func TestUserLogNanostampPrevLinkPreserved(t *testing.T) {
	sample := fixture6157(t, "user_log")
	if err := models.ValidateStrict("UserLogsResponse", sample); err != nil {
		t.Fatal(err)
	}
	var queries []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Path+"?"+r.URL.RawQuery)
		if r.URL.Query().Has("key") || r.Header.Get("Authorization") != "ApiKey mock-only" {
			t.Errorf("key must travel only in the Authorization header: %s", r.URL.RawQuery)
		}
		if r.URL.Query().Has("nanostamp") {
			w.Write([]byte(`{"log":[],"_metadata":{"links":{"prev":null,"next":null}}}`))
			return
		}
		// The live prev link, re-pointed at the mock host and carrying a key
		// that the pager must drop.
		body := bytes.Replace(sample, []byte("https://api.torn.com/v2/user/log?&limit=3"), []byte(srv.URL+"/user/log?&limit=3&key=mock-only"), 1)
		w.Write(body)
	}))
	defer srv.Close()
	c, err := torn.NewClient(torn.Config{APIKeys: []string{"mock-only"}, BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	limit := int64(3)
	pager := c.GetMyLogsPager(torn.GetMyLogsParams{Limit: &limit})
	first, ok, err := pager.Next(ctx)
	if err != nil || !ok {
		t.Fatalf("first page: %v %v", ok, err)
	}
	if !first.Value.Metadata.Nanostamp.Present || first.Value.Metadata.Nanostamp.Value != "1794235873988461998" || len(first.Value.Log) != 3 {
		t.Fatalf("nanostamp metadata: %#v", first.Value.Metadata)
	}
	if strings.Contains(first.Prev, "key=") || !strings.Contains(first.Prev, "nanostamp=1794235873988461998") {
		t.Fatalf("exposed prev link: %s", first.Prev)
	}
	if first.Value.Log[1].Details.ID != 5300 || string(first.Value.Log[1].Data["strength_before"]) != `"491781189.64563"` {
		t.Fatalf("log entry: %#v", first.Value.Log[1])
	}
	if _, ok, err := pager.Prev(ctx); err != nil || !ok {
		t.Fatalf("prev page: %v %v", ok, err)
	}
	if len(queries) != 2 || queries[0] != "/user/log?limit=3" || !strings.HasPrefix(queries[1], "/user/log?") || !strings.Contains(queries[1], "nanostamp=1794235873988461998") || !strings.Contains(queries[1], "limit=3") {
		t.Fatalf("requests: %v", queries)
	}
	// A response without the cursor still decodes (the property is optional).
	var bare models.UserLogsResponse
	if err := json.Unmarshal([]byte(`{"log":[],"_metadata":{"links":{"prev":null,"next":null}}}`), &bare); err != nil || bare.Metadata.Nanostamp.Present {
		t.Fatalf("absent nanostamp: %#v %v", bare.Metadata, err)
	}
}

func TestRemovedSearchParameterSchemasKeepDeprecatedAliases(t *testing.T) {
	if models.CompanySearchParameterVariant1Recruiting != models.GetCompaniesSearchParamFiltersItemVariant1Recruiting ||
		models.ParametersVariant1Destroyed != models.GetFactionSearchParamFiltersItemVariant1Destroyed ||
		models.UserSearchParameterVariant1LastActionNow != models.GetUsersSearchParamFiltersItemVariant1LastActionNow {
		t.Fatal("deprecated constants")
	}
	if len(models.UserSearchParameterVariant1Values()) != 24 || len(models.ParametersVariant1Values()) != 4 || len(models.CompanySearchParameterVariant1Values()) != 2 {
		t.Fatal("deprecated value lists")
	}
	company, err := models.NewCompanySearchParameterVariant2("rating:>=:5")
	if err != nil {
		t.Fatal(err)
	}
	user, err := models.NewUserSearchParameterVariant3("factions:1;2")
	if err != nil {
		t.Fatal(err)
	}
	faction, err := models.NewParametersVariant1(models.ParametersVariant1Recruiting)
	if err != nil {
		t.Fatal(err)
	}
	filters := []models.CompanySearchParameter{company}
	params := torn.GetCompaniesSearchParams{Filters: &filters}
	if (*params.Filters)[0].WhichName() == "" || user.Which() != 3 || faction.Which() != 1 {
		t.Fatalf("deprecated unions: %s %d %d", (*params.Filters)[0].WhichName(), user.Which(), faction.Which())
	}
	for _, removed := range []string{"CompanySearchParameter", "Parameters", "UserSearchParameter"} {
		for _, name := range models.SchemaNames() {
			if name == removed {
				t.Errorf("%s is no longer an official 6.15.7 schema", removed)
			}
		}
	}
}
