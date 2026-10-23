// Package contract validates pinned Torn OpenAPI contracts without numeric coercion.
package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"regexp"
	"strings"
)

type Schema map[string]any
type Registry map[string]Schema

func Load(data []byte) Registry {
	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		panic(err)
	}
	return r
}
func (r Registry) Validate(name string, data []byte) error {
	s, ok := r[name]
	if !ok {
		return fmt.Errorf("unknown schema %s", name)
	}
	return r.ValidateSchema(s, data)
}
func (r Registry) ValidateSchema(s Schema, data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, e := read(d)
	if e != nil {
		return e
	}
	if _, e = d.Token(); e != io.EOF {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return r.check(s, v, "$")
}

// read deliberately rejects duplicate names and preserves all numbers as json.Number.
func read(d *json.Decoder) (any, error) {
	t, e := d.Token()
	if e != nil {
		return nil, e
	}
	if x, ok := t.(json.Delim); ok {
		switch x {
		case '{':
			m := map[string]any{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, e
				}
				key := k.(string)
				if _, ok := m[key]; ok {
					return nil, fmt.Errorf("duplicate JSON property %q", key)
				}
				v, e := read(d)
				if e != nil {
					return nil, e
				}
				m[key] = v
			}
			_, e = d.Token()
			return m, e
		case '[':
			a := []any{}
			for d.More() {
				v, e := read(d)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			_, e = d.Token()
			return a, e
		}
	}
	return t, nil
}
func (r Registry) check(s Schema, v any, p string) error {
	if ref, ok := s["$ref"].(string); ok {
		return r.check(r[strings.TrimPrefix(ref, "#/components/schemas/")], v, p)
	}
	if v == nil {
		if s["nullable"] == true || s["type"] == "null" {
			return nil
		}
		if _, ok := s["type"]; !ok {
			if _, a := s["oneOf"]; !a {
				if _, b := s["anyOf"]; !b {
					if _, c := s["allOf"]; !c {
						return nil
					}
				}
			}
		}
	}
	for _, k := range []string{"oneOf", "anyOf"} {
		if a, ok := s[k].([]any); ok {
			for _, b := range a {
				if r.check(Schema(b.(map[string]any)), v, p) == nil {
					return nil
				}
			}
			return fmt.Errorf("%s: no %s variant matches", p, k)
		}
	}
	if a, ok := s["allOf"].([]any); ok {
		for _, b := range a {
			if e := r.check(Schema(b.(map[string]any)), v, p); e != nil {
				return e
			}
		}
	}
	if a, ok := s["enum"].([]any); ok {
		found := false
		for _, x := range a {
			if equal(x, v) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s: invalid enum value", p)
		}
	}
	switch s["type"] {
	case "null":
		if v != nil {
			return fmt.Errorf("%s: expected null", p)
		}
	case "string":
		x, ok := v.(string)
		if !ok {
			return fmt.Errorf("%s: expected string", p)
		}
		if n, ok := s["minLength"].(float64); ok && len([]rune(x)) < int(n) {
			return fmt.Errorf("%s: too short", p)
		}
		if n, ok := s["maxLength"].(float64); ok && len([]rune(x)) > int(n) {
			return fmt.Errorf("%s: too long", p)
		}
		if pattern, ok := s["pattern"].(string); ok {
			re, e := regexp.Compile(pattern)
			if e == nil && !re.MatchString(x) {
				return fmt.Errorf("%s: pattern mismatch", p)
			}
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s: expected boolean", p)
		}
	case "integer", "number":
		x, ok := v.(json.Number)
		if !ok {
			return fmt.Errorf("%s: expected number", p)
		}
		var q *big.Rat
		if s["type"] == "integer" {
			n, e := integerValue(x)
			if e != nil {
				return fmt.Errorf("%s: %s", p, e)
			}
			q = new(big.Rat).SetInt64(n)
		} else {
			if _, lo := s["minimum"]; !lo {
				if _, hi := s["maximum"]; !hi {
					return nil
				}
			}
			var valid bool
			q, valid = new(big.Rat).SetString(string(x))
			if !valid {
				return fmt.Errorf("%s: invalid number", p)
			}
		}
		for _, k := range []string{"minimum", "maximum"} {
			if n, ok := s[k]; ok {
				b, ok := new(big.Rat).SetString(fmt.Sprint(n))
				if ok {
					cmp := q.Cmp(b)
					if k == "minimum" && cmp < 0 || k == "maximum" && cmp > 0 {
						return fmt.Errorf("%s: outside %s", p, k)
					}
				}
			}
		}
	case "array":
		a, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s: expected array", p)
		}
		if n, ok := s["minItems"].(float64); ok && len(a) < int(n) {
			return fmt.Errorf("%s: too few items", p)
		}
		if n, ok := s["maxItems"].(float64); ok && len(a) > int(n) {
			return fmt.Errorf("%s: too many items", p)
		}
		if s["uniqueItems"] == true {
			for i := range a {
				for j := 0; j < i; j++ {
					if reflect.DeepEqual(a[i], a[j]) {
						return fmt.Errorf("%s: duplicate array item", p)
					}
				}
			}
		}
		if items, ok := s["items"].(map[string]any); ok {
			for i, x := range a {
				if e := r.check(Schema(items), x, fmt.Sprintf("%s[%d]", p, i)); e != nil {
					return e
				}
			}
		}
	case "object":
		if _, ok := v.(map[string]any); !ok {
			return fmt.Errorf("%s: expected object", p)
		}
	}
	if m, ok := v.(map[string]any); ok {
		if req, ok := s["required"].([]any); ok {
			for _, x := range req {
				if _, ok := m[x.(string)]; !ok {
					return fmt.Errorf("%s: missing required property %s", p, x)
				}
			}
		}
		props, _ := s["properties"].(map[string]any)
		for k, x := range m {
			if prop, ok := props[k].(map[string]any); ok {
				if e := r.check(Schema(prop), x, p+"."+k); e != nil {
					return e
				}
			} else if add, ok := s["additionalProperties"].(map[string]any); ok {
				if e := r.check(Schema(add), x, p+"."+k); e != nil {
					return e
				}
			} else if s["additionalProperties"] == false {
				return fmt.Errorf("%s: unknown property %s", p, k)
			}
		}
	}
	return nil
}
func equal(a, b any) bool {
	if n, ok := b.(json.Number); ok {
		v, e := json.Marshal(a)
		if e != nil {
			return false
		}
		left, le := integerValue(json.Number(v))
		right, re := integerValue(n)
		if le == nil && re == nil {
			return left == right
		}
		return string(v) == string(n)
	}
	return reflect.DeepEqual(a, b)
}
