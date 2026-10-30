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

// travelInventorySample is a scrubbed live /v2/user/travelinventory payload.
// It contains no credential and covers both a concrete and a null item uid.
const travelInventorySample = `{"travelinventory":{"capacity":{"base":28,"special":0},"inventory":[{"id":732365689,"item":{"id":177,"uid":18545986362,"name":"Cobra Derringer","type":"Secondary","is_equipped":true,"is_faction_owned":false,"is_permanent":true}},{"id":742823284,"item":{"id":738,"uid":null,"name":"Blood Bag : O+","type":"Medical","is_equipped":false,"is_faction_owned":false,"is_permanent":true}}]}}`

func TestCurrentOperationMetadataMatchesOfficialContract(t *testing.T) {
	data, err := os.ReadFile("spec/openapi-6.15.0.json")
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
					access = name
				}
			}
			levels[access]++
			got, ok := torn.MetadataForOperation(op.ID)
			if !ok || got.Stability != op.Stability || got.AccessLevel != access {
				t.Errorf("%s metadata: %#v, %v; want %q / %q", op.ID, got, ok, op.Stability, access)
			}
		}
	}
	if count != 237 || len(torn.Operations) != 237 {
		t.Fatalf("metadata inventory: %d spec, %d SDK", count, len(torn.Operations))
	}
	if levels[""] != 0 || levels["Public"] == 0 || levels["Minimal"] == 0 || levels["Limited"] == 0 || levels["Full"] == 0 {
		t.Fatalf("access level inventory: %v", levels)
	}
	got, _ := torn.MetadataForOperation("getMyTravelInventory")
	if got != (torn.OperationMetadata{Stability: "Unstable", AccessLevel: "Limited"}) {
		t.Fatalf("travel inventory metadata: %#v", got)
	}
	if torn.APIVersion != "6.15.0" {
		t.Fatalf("APIVersion %q", torn.APIVersion)
	}
}

func TestTravelInventoryLiveSampleDecodes(t *testing.T) {
	payload := []byte(travelInventorySample)
	if err := models.Validate("UserTravelInventoryResponse", payload); err != nil {
		t.Fatal(err)
	}
	var response models.UserTravelInventoryResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	inventory := response.Travelinventory
	if inventory.Capacity.Base != 28 || inventory.Capacity.Special != 0 || len(inventory.Inventory) != 2 {
		t.Fatalf("travel inventory shape: %#v", inventory)
	}
	first, err := inventory.Inventory[0].Item.AsVariant1()
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Inventory[0].ID != 732365689 || first.ID != 177 || first.Name != "Cobra Derringer" || first.Type != "Secondary" || !first.IsEquipped || first.IsFactionOwned || !first.IsPermanent {
		t.Fatalf("first slot: %#v", first)
	}
	uid, err := first.Uid.AsVariant1()
	if err != nil || uid != 18545986362 {
		t.Fatalf("first uid %d: %v", uid, err)
	}
	if _, err := first.Uid.AsVariant2(); err == nil {
		t.Fatal("concrete uid accepted as null")
	}
	second, err := inventory.Inventory[1].Item.AsVariant1()
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != 738 || second.Name != "Blood Bag : O+" || second.IsEquipped {
		t.Fatalf("second slot: %#v", second)
	}
	if _, err := second.Uid.AsVariant1(); err == nil {
		t.Fatal("null uid accepted as ItemUid")
	}
	if _, err := second.Uid.AsVariant2(); err != nil || string(second.Uid.RawJSON()) != "null" {
		t.Fatalf("null uid lost: %s %v", second.Uid.RawJSON(), err)
	}
	encoded, err := json.Marshal(response)
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

func TestTravelInventoryNullItemAndRejections(t *testing.T) {
	var slot models.UserTravelInventorySlot
	if err := json.Unmarshal([]byte(`{"id":1,"item":null}`), &slot); err != nil {
		t.Fatal(err)
	}
	if _, err := slot.Item.AsVariant2(); err != nil {
		t.Fatal(err)
	}
	if _, err := slot.Item.AsVariant1(); err == nil {
		t.Fatal("null item accepted as UserTravelInventoryItem")
	}
	encoded, err := json.Marshal(slot)
	if err != nil || !bytes.Contains(encoded, []byte(`"item":null`)) {
		t.Fatalf("null item round trip: %s %v", encoded, err)
	}
	for _, invalid := range []string{
		`{"id":1}`,
		`{"id":1,"item":{"id":177,"uid":null,"name":"x","type":"y","is_equipped":true,"is_faction_owned":false}}`,
		`{"id":1,"item":{"id":177,"uid":"18545986362","name":"x","type":"y","is_equipped":true,"is_faction_owned":false,"is_permanent":true}}`,
		`{"id":1,"item":{"id":177,"name":"x","type":"y","is_equipped":true,"is_faction_owned":false,"is_permanent":true}}`,
	} {
		if err := json.Unmarshal([]byte(invalid), &slot); err == nil {
			t.Errorf("accepted malformed slot: %s", invalid)
		}
	}
	var capacity models.UserTravelInventoryCapacity
	if err := json.Unmarshal([]byte(`{"base":28}`), &capacity); err == nil {
		t.Fatal("accepted capacity without special")
	}
}

func TestEnumAdditions6150(t *testing.T) {
	var position models.JobPositionCasinoEnum
	if err := json.Unmarshal([]byte(`"Dealer"`), &position); err != nil || position != models.JobPositionCasinoEnumDealer {
		t.Fatalf("Dealer: %q %v", position, err)
	}
	var result models.FactionTerritoryWarResultEnum
	if err := json.Unmarshal([]byte(`"end_with_dirty_bomb"`), &result); err != nil || result != models.FactionTerritoryWarResultEnumEndWithDirtyBomb {
		t.Fatalf("end_with_dirty_bomb: %q %v", result, err)
	}
	if err := json.Unmarshal([]byte(`"dealer"`), &position); err == nil {
		t.Fatal("enum matching must stay case-sensitive")
	}
	if err := json.Unmarshal([]byte(`"end_with_bomb"`), &result); err == nil {
		t.Fatal("accepted undocumented territory war result")
	}
	for name, value := range map[string]string{"JobPositionCasinoEnum": `"Dealer"`, "FactionTerritoryWarResultEnum": `"end_with_dirty_bomb"`} {
		if err := models.Validate(name, []byte(value)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTravelInventoryRoutingAndTypedBatch(t *testing.T) {
	var capability *torn.SelectionCapability
	for _, c := range torn.Capabilities() {
		if c.Resource == "user" && c.Name == "travelinventory" {
			c := c
			capability = &c
		}
	}
	if capability == nil || !capability.Generic || capability.Standalone || capability.Legacy || capability.CSV || len(capability.Endpoints) != 1 || capability.Endpoints[0].Path != "/user/travelinventory" || capability.Endpoints[0].Schema != "UserTravelInventoryResponse" {
		t.Fatalf("travelinventory capability: %#v", capability)
	}
	plan, err := torn.PlanSelections("user", []string{"travelinventory", "job"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Calls) != 1 || plan.Calls[0].Operation.Path != "/user" || !reflect.DeepEqual(plan.Calls[0].Selections, []string{"job", "travelinventory"}) {
		t.Fatalf("combined plan: %+v", plan)
	}

	doc := loadContract(t, "6.15.0")
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(travelInventorySample), &payload); err != nil {
		t.Fatal(err)
	}
	for k, v := range contractSample(doc.Components.Schemas["UserJobResponse"], doc.Components.Schemas, 1, 0).(map[string]any) {
		payload[k] = v
	}
	raw, _ := json.Marshal(payload)
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.Query().Get("selections"))
		if r.Header.Get("Authorization") != "ApiKey mock-only" || r.URL.Query().Get("key") != "" {
			t.Errorf("key must travel only in the Authorization header")
		}
		if r.URL.Path == "/user/travelinventory" {
			w.Write([]byte(travelInventorySample))
			return
		}
		w.Write(raw)
	}))
	defer srv.Close()
	c, err := torn.NewClient(torn.Config{APIKeys: []string{"mock-only"}, BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	batch, travel := torn.NewBatch(torn.UserSelections.Travelinventory)
	job := torn.AddSelection(batch, torn.UserSelections.Job)
	report, err := batch.Execute(ctx, c, nil, torn.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Calls) != 1 || len(paths) != 1 || paths[0] != "/user?job,travelinventory" {
		t.Fatalf("typed batch requests: %v", paths)
	}
	value, err := travel.Value()
	if err != nil || value.Travelinventory.Capacity.Base != 28 || len(value.Travelinventory.Inventory) != 2 {
		t.Fatalf("typed batch travel inventory: %#v %v", value, err)
	}
	if _, err := job.Value(); err != nil {
		t.Fatal(err)
	}
	helper, _, err := c.UserTravelinventory(ctx, nil, torn.RequestOptions{})
	if err != nil || helper.Travelinventory.Capacity.Base != 28 {
		t.Fatalf("typed helper: %#v %v", helper, err)
	}
	exact, _, err := c.GetMyTravelInventory(ctx, torn.GetMyTravelInventoryParams{})
	if err != nil || len(exact.Travelinventory.Inventory) != 2 {
		t.Fatalf("exact operation: %#v %v", exact, err)
	}
	if paths[len(paths)-1] != "/user/travelinventory?" {
		t.Fatalf("exact operation path: %v", paths)
	}
}
