package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// integerValue accepts JSON's decimal/exponent notation without floating point
// conversion or allocating space proportional to an untrusted exponent.
func integerValue(n json.Number) (int64, error) {
	s := string(n)
	negative := strings.HasPrefix(s, "-")
	if negative {
		s = s[1:]
	}
	exponentText := "0"
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exponentText = s[i+1:]
		s = s[:i]
	}
	fraction := 0
	if i := strings.IndexByte(s, '.'); i >= 0 {
		fraction = len(s) - i - 1
		s = s[:i] + s[i+1:]
	}
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return 0, nil
	}
	exponent, e := strconv.ParseInt(exponentText, 10, 64)
	if e != nil {
		return 0, fmt.Errorf("integer exponent outside supported domain")
	}
	if exponent < -int64(len(s))-int64(fraction) || exponent > 20+int64(fraction) {
		return 0, fmt.Errorf("integer outside signed 64-bit domain")
	}
	shift := exponent - int64(fraction)
	if shift < 0 {
		remove := -shift
		if remove > int64(len(s)) {
			return 0, fmt.Errorf("expected integer")
		}
		suffix := s[len(s)-int(remove):]
		if strings.Trim(suffix, "0") != "" {
			return 0, fmt.Errorf("expected integer")
		}
		s = s[:len(s)-int(remove)]
	} else {
		if int64(len(s))+shift > 19 {
			return 0, fmt.Errorf("integer outside signed 64-bit domain")
		}
		s += strings.Repeat("0", int(shift))
	}
	if s == "" {
		s = "0"
	}
	if negative {
		s = "-" + s
	}
	v, e := strconv.ParseInt(s, 10, 64)
	if e != nil {
		return 0, fmt.Errorf("integer outside signed 64-bit domain")
	}
	return v, nil
}

// Normalize validates and canonicalizes only schema-declared integer values.
// This permits exact decoding of JSON-schema integers written as 1.0 or 1e3
// into Go int64 fields while preserving arbitrary unknown/decimal JSON numbers.
func (r Registry) Normalize(name string, data []byte) ([]byte, error) {
	s, ok := r[name]
	if !ok {
		return nil, fmt.Errorf("unknown schema %s", name)
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, e := read(d)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, fmt.Errorf("unexpected trailing JSON")
	}
	if e = r.check(s, v, "$"); e != nil {
		return nil, e
	}
	return json.Marshal(r.normalize(s, v))
}
func (r Registry) normalize(s Schema, v any) any {
	if ref, ok := s["$ref"].(string); ok {
		return r.normalize(r[strings.TrimPrefix(ref, "#/components/schemas/")], v)
	}
	if v == nil {
		return v
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		if a, ok := s[key].([]any); ok {
			for _, branch := range a {
				b := Schema(branch.(map[string]any))
				if r.check(b, v, "$") == nil {
					return r.normalize(b, v)
				}
			}
		}
	}
	if a, ok := s["allOf"].([]any); ok {
		for _, branch := range a {
			v = r.normalize(Schema(branch.(map[string]any)), v)
		}
	}
	if s["type"] == "integer" {
		n, _ := integerValue(v.(json.Number))
		return json.Number(strconv.FormatInt(n, 10))
	}
	if m, ok := v.(map[string]any); ok {
		props, _ := s["properties"].(map[string]any)
		for k, value := range m {
			if p, ok := props[k].(map[string]any); ok {
				m[k] = r.normalize(Schema(p), value)
			} else if add, ok := s["additionalProperties"].(map[string]any); ok {
				m[k] = r.normalize(Schema(add), value)
			}
		}
	}
	if a, ok := v.([]any); ok {
		if items, ok := s["items"].(map[string]any); ok {
			for i, value := range a {
				a[i] = r.normalize(Schema(items), value)
			}
		}
	}
	return v
}
