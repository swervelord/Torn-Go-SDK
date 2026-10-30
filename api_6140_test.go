package torn_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	torn "github.com/swervelord/Torn-Go-SDK"
	"github.com/swervelord/Torn-Go-SDK/models"
)

func TestOperationMetadataMatchesOfficialStability(t *testing.T) {
	data, err := os.ReadFile("spec/openapi-6.14.0.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			ID        string `json:"operationId"`
			Stability string `json:"x-stability"`
		}
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, methods := range doc.Paths {
		for _, op := range methods {
			if op.ID == "" {
				continue
			}
			count++
			got, ok := torn.MetadataForOperation(op.ID)
			if !ok || got.Stability != op.Stability {
				t.Errorf("%s stability: %#v, %v; want %q", op.ID, got, ok, op.Stability)
			}
		}
	}
	if count != 236 {
		t.Fatalf("metadata inventory: %d", count)
	}
	for id, seconds := range map[string]int{"getMyInventory": 900, "getMyFactionInventory": 300} {
		got, ok := torn.MetadataForOperation(id)
		if !ok || got.CacheTTLSeconds != seconds {
			t.Errorf("%s cache: %#v", id, got)
		}
	}
	if _, ok := torn.MetadataForOperation("unknown"); ok {
		t.Fatal("unknown operation metadata accepted")
	}
	got, _ := torn.MetadataForOperation("getMyHalloween")
	if got.Stability != "Untable" {
		t.Fatal("upstream spelling must remain intact")
	}
	if torn.UserInventoryCacheTTLSeconds != 900 || torn.FactionInventoryCacheTTLSeconds != 300 {
		t.Fatal("contract constants")
	}
}

func TestHalloweenTreatsAndUpgradeFidelity(t *testing.T) {
	payload := []byte(`{"halloween":{"treats":{"current":45,"total":9007199254740993},"basket_upgrades":[{"id":12,"timestamp":1793116800}]}}`)
	var response models.UserHalloweenResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if response.Halloween.Treats.Total != 9007199254740993 || response.Halloween.Treats.Current != 45 || response.Halloween.BasketUpgrades[0].ID != 12 {
		t.Fatalf("wrong typed treats: %#v", response)
	}
	for _, invalid := range []string{
		`{"halloween":{"treats":{"current":45},"basket_upgrades":[]}}`,
		`{"halloween":{"treats":{"current":45,"total":90},"basket_upgrades":[{"id":"12","timestamp":1793116800}]}}`,
	} {
		if err := json.Unmarshal([]byte(invalid), &response); err == nil {
			t.Fatalf("accepted malformed treats: %s", invalid)
		}
	}
	payload = []byte(`{"id":12,"name":"Test","description":"Fixture","flavor":"Spooky","cost":10,"requirement":0,"type":{"future":9007199254740993},"extension":false}`)
	var upgrade models.TornHalloweenUpgrade
	if err := json.Unmarshal(payload, &upgrade); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(upgrade.Type, []byte("9007199254740993")) {
		t.Fatal("unspecified required type lost precision")
	}
	encoded, err := json.Marshal(upgrade)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"extension":false`)) || !bytes.Contains(encoded, []byte(`"future":9007199254740993`)) {
		t.Fatalf("lost raw fields: %s", encoded)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "type")
	missing, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(missing, &upgrade); err == nil {
		t.Fatal("accepted missing required type")
	}
}
