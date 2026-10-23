package torn_test

import (
	"bytes"
	"encoding/json"
	"github.com/swervelord/Torn-Go-SDK/legacy"
	"github.com/swervelord/Torn-Go-SDK/models"
	"testing"
)

// These fixtures are manually written examples of documented wire boundaries;
// no account responses or credentials are captured here.
func TestDocumentedTravelNullablePresence(t *testing.T) {
	valid := `{"travel":{"destination":"Torn","method":null,"departed_at":null,"arrival_at":null,"time_left":0}}`
	var value models.UserTravelResponse
	if e := json.Unmarshal([]byte(valid), &value); e != nil {
		t.Fatal(e)
	}
	if value.Travel.TimeLeft != 0 {
		t.Fatal("zero changed")
	}
	if raw := value.Travel.Method.RawJSON(); !bytes.Equal(raw, []byte("null")) {
		t.Fatalf("required null lost %s", raw)
	}
	for _, raw := range []string{`{}`, `{"travel":null}`, `{"travel":{"destination":"Torn","departed_at":null,"arrival_at":null,"time_left":0}}`, `{"travel":{"destination":"Torn","method":null,"departed_at":null,"arrival_at":null,"time_left":null}}`, `{"travel":{"destination":"Torn","method":null,"departed_at":null,"arrival_at":null,"time_left":1.1}}`} {
		if e := json.Unmarshal([]byte(raw), &value); e == nil {
			t.Fatalf("invalid nullable presence accepted: %s", raw)
		}
	}
}
func TestOptionalStatesAndExactUnknownFields(t *testing.T) {
	var missing models.Optional[bool]
	if missing.Present {
		t.Fatal("zero optional present")
	}
	for _, tc := range []struct {
		raw         string
		null, value bool
	}{{"null", true, false}, {"false", false, false}, {"true", false, true}} {
		var out models.Optional[bool]
		if e := json.Unmarshal([]byte(tc.raw), &out); e != nil {
			t.Fatal(e)
		}
		if !out.Present || out.Null != tc.null || out.Value != tc.value {
			t.Fatalf("%s: %+v", tc.raw, out)
		}
		raw, e := json.Marshal(out)
		if e != nil || string(raw) != tc.raw {
			t.Fatalf("roundtrip %s %s %v", tc.raw, raw, e)
		}
	}
	const fixture = `{"job":null,"Job":{"future":"opaque","n":9007199254740993},"future":9007199254740993}`
	var response models.UserJobResponse
	if e := json.Unmarshal([]byte(fixture), &response); e != nil {
		t.Fatal(e)
	}
	if string(response.Job.RawJSON()) != "null" {
		t.Fatal("case-insensitive matching corrupted job")
	}
	if string(response.AdditionalProperties["future"]) != "9007199254740993" {
		t.Fatal("extra integer lost precision")
	}
	out, e := json.Marshal(response)
	if e != nil {
		t.Fatal(e)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(out, &fields)
	if string(fields["Job"]) != `{"future":"opaque","n":9007199254740993}` {
		t.Fatalf("case-sensitive extra changed: %s", out)
	}
}
func TestStrictEnumsDuplicatesAndExactNumbers(t *testing.T) {
	for _, raw := range []string{`{"job":null,"job":null}`, `{"JOB":null}`, `{"job":false}`, `{"job":null} {}`} {
		var value models.UserJobResponse
		if e := json.Unmarshal([]byte(raw), &value); e == nil {
			t.Fatalf("invalid shape accepted: %s", raw)
		}
	}
	var n models.UserId
	if e := json.Unmarshal([]byte(`9007199254740993`), &n); e != nil || int64(n) != 9007199254740993 {
		t.Fatalf("integer precision: %d %v", n, e)
	}
	var code models.ErrorNoCompetitionAvailable
	if e := json.Unmarshal([]byte(`{"code":33,"error":"No competition available"}`), &code); e != nil {
		t.Fatal(e)
	}
	if e := json.Unmarshal([]byte(`{"code":34,"error":"wrong enum"}`), &code); e == nil {
		t.Fatal("closed code enum accepted wrong code")
	}
}
func TestCurrentAndFrozenTeamContractCannotBeConfused(t *testing.T) {
	const common = `"id":1,"name":"Example","participants":0,"participants_left":0,"position":0,"score":0,"lives":0,"wins":0,"losses":0,"eliminated":false,"eliminated_timestamp":null,"attacking_summary":[]`
	current := []byte(`{` + common + `,"captains":{"captain":null,"vice_captains":[]}}`)
	historical := []byte(`{` + common + `,"leaders":[]}`)
	var newModel models.TornEliminationTeam
	var oldModel legacy.TornEliminationTeam
	if e := json.Unmarshal(current, &newModel); e != nil {
		t.Fatal(e)
	}
	if e := json.Unmarshal(historical, &newModel); e == nil {
		t.Fatal("legacy leaders masqueraded as current captains")
	}
	if e := json.Unmarshal(current, &oldModel); e == nil {
		t.Fatal("current payload masqueraded as historical")
	}
	// Historical leaders shape is independently sampled from the historical
	// snapshot in concrete_models_test.go; only the migration rejection is asserted here.
}
func FuzzUserJobUnionRoundTrip(f *testing.F) {
	for _, s := range []string{`{"job":null}`, `{"job":{"type":"job","name":"Army","position":"Private"}}`, `{"job":false}`, `{"job":null,"extra":9007199254740993}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var value models.UserJobResponse
		if json.Unmarshal(data, &value) != nil {
			return
		}
		out, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		if e = models.Validate("UserJobResponse", out); e != nil {
			t.Fatalf("accepted model emitted invalid JSON: %v", e)
		}
	})
}
func TestIntegralExponentAndDecimalDecoding(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		value int64
	}{{"1e3", 1000}, {"1.0", 1}, {"9.007199254740993e15", 9007199254740993}, {"-9.223372036854775808e18", -9223372036854775808}, {"0e9999999999999999999999", 0}, {"-0.000e-999999999999999999999", 0}} {
		var value models.UserId
		if e := json.Unmarshal([]byte(tc.raw), &value); e != nil {
			t.Fatalf("%s: %v", tc.raw, e)
		}
		if int64(value) != tc.value {
			t.Fatalf("%s: got %d", tc.raw, value)
		}
	}
	// Nested concrete integer fields and arrays must use the same semantics.
	var travel models.UserTravelResponse
	if e := json.Unmarshal([]byte(`{"travel":{"destination":"Torn","method":null,"departed_at":null,"arrival_at":null,"time_left":1e3}}`), &travel); e != nil || travel.Travel.TimeLeft != 1000 {
		t.Fatalf("nested exponent: %+v %v", travel, e)
	}
	var code models.ErrorNoCompetitionAvailable
	if e := json.Unmarshal([]byte(`{"code":3.3e1,"error":"No competition available"}`), &code); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{"1e99999999999999999", "1e-9999999999999999", "1.1", "9223372036854775808", "-9223372036854775809"} {
		var value models.UserId
		if e := json.Unmarshal([]byte(raw), &value); e == nil {
			t.Fatalf("invalid integer %s accepted", raw)
		}
	}
}
func TestUnionConstructorsValidateSelectedBranch(t *testing.T) {
	if _, e := models.NewUserProfileResponseProfileRankVariant1(models.UserRankEnum("future-unknown-rank")); e == nil {
		t.Fatal("closed enum constructor escaped through permissive string sibling")
	}
	union, e := models.NewUserProfileResponseProfileRankVariant2("future-unknown-rank")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = union.AsVariant1(); e == nil {
		t.Fatal("closed enum accessor accepted unknown string")
	}
	v, e := union.AsVariant2()
	if e != nil || v != "future-unknown-rank" {
		t.Fatalf("permissive string branch %s %v", v, e)
	}
}
func FuzzIntegerModelExactness(f *testing.F) {
	for _, s := range []string{"9007199254740993", "1e3", "1.0", "1e999999999999999999999", "0e999999999999999999999", "-9223372036854775808", "9223372036854775807"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		var v models.UserId
		err := json.Unmarshal([]byte(raw), &v)
		valid := models.Validate("UserId", []byte(raw))
		if (err == nil) != (valid == nil) {
			t.Fatalf("validator/concrete divergence %q: decode=%v validate=%v", raw, err, valid)
		}
		if err == nil {
			out, e := json.Marshal(v)
			if e != nil || models.Validate("UserId", out) != nil {
				t.Fatalf("invalid exact integer roundtrip %s %v", out, e)
			}
		}
	})
}
