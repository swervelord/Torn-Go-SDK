package compat

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCompatibilityIntegerSpellings(t *testing.T) {
	cases := []struct {
		name, input, want string
		unsigned, reject  bool
	}{
		{"unsigned decimal", `1.0`, `1`, true, false},
		{"unsigned exponent", `1e3`, `1000`, true, false},
		{"unsigned maximum decimal", `18446744073709551615.0`, `18446744073709551615`, true, false},
		{"unsigned maximum scientific", `1.8446744073709551615e19`, `18446744073709551615`, true, false},
		{"unsigned maximum negative exponent", `184467440737095516150e-1`, `18446744073709551615`, true, false},
		{"unsigned overflow scientific", `1.8446744073709551616e19`, ``, true, true},
		{"unsigned negative exponent fractional", `1e-1`, ``, true, true},
		{"unsigned negative", `-1.0`, ``, true, true},
		{"unsigned negative zero", `-0.0e3`, `0`, true, false},
		{"signed minimum scientific", `-9.223372036854775808e18`, `-9223372036854775808`, false, false},
		{"signed maximum decimal", `9223372036854775807.0`, `9223372036854775807`, false, false},
		{"signed overflow decimal", `9223372036854775808.0`, ``, false, true},
		{"signed underflow exponent", `-9223372036854775809e0`, ``, false, true},
		{"signed fractional mantissa", `1.0000000000000000001`, ``, false, true},
		{"signed integral negative exponent", `100e-2`, `1`, false, false},
		{"huge positive exponent bounded", `1e9223372036854775807`, ``, true, true},
		{"huge negative exponent bounded", `1e-9223372036854775808`, ``, false, true},
		{"huge unparseable exponent bounded", `1e999999999999999999999999`, ``, true, true},
		{"zero huge exponent bounded", `0e999999999999999999999999`, `0`, true, false},
		{"quoted number rejected", `"1e3"`, ``, false, true},
		{"null concrete rejected", `null`, ``, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var target any
			if tc.unsigned {
				target = new(uint64)
			} else {
				target = new(int64)
			}
			e := decodeCompat([]byte(tc.input), target)
			if tc.reject {
				if e == nil {
					t.Fatal("non-integral or out-of-range number accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			b, e := json.Marshal(target)
			if e != nil || string(b) != tc.want {
				t.Fatalf("want %s got %s: %v", tc.want, b, e)
			}
		})
	}
}

func TestCompatibilityNestedIntegerSpellings(t *testing.T) {
	var item UserInventoryItem
	if e := json.Unmarshal([]byte(`{"id":1.8446744073709551615e19,"amount":3.0}`), &item); e != nil {
		t.Fatal(e)
	}
	if item.ID == nil || *item.ID != 18446744073709551615 || item.Amount == nil || *item.Amount != 3 {
		t.Fatal("optional typed integer pointer not normalized")
	}
	var nested map[string][]*uint64
	if e := decodeCompat([]byte(`{"values":[1e3,null,184467440737095516150e-1]}`), &nested); e != nil {
		t.Fatal(e)
	}
	if len(nested["values"]) != 3 || *nested["values"][0] != 1000 || nested["values"][1] != nil || *nested["values"][2] != 18446744073709551615 {
		t.Fatal("map/array integer leaves not normalized")
	}
	if e := decodeCompat([]byte(`{"values":[1.01]}`), &nested); e == nil {
		t.Fatal("nested fractional integer accepted")
	}
	var dynamic DynamicValue
	if e := json.Unmarshal([]byte(`1e3`), &dynamic); e != nil || dynamic.Int64 == nil || *dynamic.Int64 != 1000 {
		t.Fatalf("numeric union variant: %v %+v", e, dynamic)
	}
	var employee CompanyProfileEmployee
	if e := json.Unmarshal([]byte(`184467440737095516150e-1`), &employee); e != nil || employee.Uint64 == nil || *employee.Uint64 != 18446744073709551615 {
		t.Fatalf("unsigned union variant: %v %+v", e, employee)
	}
	var inventory UserInventory
	if e := json.Unmarshal([]byte(`{"items":[{"id":1e3}],"unknown":1e3}`), &inventory); e != nil {
		t.Fatal(e)
	}
	if string(inventory.Extra["unknown"]) != "1e3" {
		t.Fatal("unknown JSON spelling changed")
	}
	raw, e := json.Marshal(inventory)
	if e != nil || !strings.Contains(string(raw), `"unknown":1e3`) {
		t.Fatal("raw unknown JSON changed on marshal")
	}
	var trade TradeItem
	if e := json.Unmarshal([]byte(`{"type":"Future","user_id":1e3}`), &trade); e != nil || trade.UnknownTradeItem == nil || trade.UnknownTradeItem.UserID == nil || *trade.UnknownTradeItem.UserID != 1000 {
		t.Fatalf("special union optional integer: %v", e)
	}
}

type strictInteger int64

var errCustomInteger = errors.New("custom decoder owns validation")

func (*strictInteger) UnmarshalJSON([]byte) error { return errCustomInteger }
func TestCompatibilityIntegerCustomDecoderPrecedence(t *testing.T) {
	var v strictInteger
	if e := decodeCompat([]byte(`1e3`), &v); !errors.Is(e, errCustomInteger) {
		t.Fatalf("custom unmarshaler bypassed: %v", e)
	}
	var raw json.RawMessage
	if e := decodeCompat([]byte(`1e3`), &raw); e != nil || !reflect.DeepEqual(raw, json.RawMessage(`1e3`)) {
		t.Fatalf("raw JSON changed: %s %v", raw, e)
	}
}

func FuzzCompatibilityIntegerNotation(f *testing.F) {
	for _, s := range []string{"1e3", "18446744073709551615.0", "1e99999999999999999999999", "100e-2"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		var v uint64
		if decodeCompat([]byte(s), &v) == nil {
			raw, e := json.Marshal(v)
			if e != nil || !json.Valid(raw) {
				t.Fatal("invalid normalized integer")
			}
		}
	})
}
