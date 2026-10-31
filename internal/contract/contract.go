// Package contract validates and decodes pinned Torn OpenAPI contracts without
// numeric coercion.
//
// Decoding is tolerant by default (PARITY_SPEC P1.1-P1.3): unknown enum values,
// unknown properties, schema constraints and fields relaxed by
// spec/shared/spec-overrides.json are accepted. Strict validation reports all
// of them and is used by tests, CI and the client's opt-in strict mode.
package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"math/bits"
	"reflect"
	"regexp"
	"strings"
	"sync"
)

// Schema is one JSON schema object from a pinned contract.
type Schema map[string]any

// Registry maps component and named inline schema names to their schemas.
type Registry map[string]Schema

// RelaxedKey lists required properties made optional by spec overrides (P1.2).
// Strict validation still reports their absence.
const RelaxedKey = "x-sdk-relaxed-required"

// Mode selects which schema rules a validation enforces.
type Mode struct {
	// Enums rejects values outside a schema's enum list.
	Enums bool
	// Constraints enforces pattern, length, range, item-count and uniqueness rules.
	Constraints bool
	// UnknownProperties rejects object members the schema does not declare.
	UnknownProperties bool
	// Relaxed reports required properties relaxed by spec overrides when absent.
	Relaxed bool
}

var (
	// Tolerant is the default decode mode: only JSON types and required
	// properties are enforced (P1.3).
	Tolerant = Mode{}
	// Params validates request parameters: constraints are enforced, enum
	// values are forwarded so new upstream values keep working (P1.5).
	Params = Mode{Constraints: true}
	// Discriminate is used first when choosing union variants, so enum-tagged
	// branches are told apart before tolerant matching is attempted.
	Discriminate = Mode{Enums: true, Constraints: true}
	// Strict enforces everything (P1.3 opt-in strict mode).
	Strict = Mode{Enums: true, Constraints: true, UnknownProperties: true, Relaxed: true}
)

// Load parses an embedded schema registry.
func Load(data []byte) Registry {
	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		panic(err)
	}
	return r
}

// Validate checks data against the named schema in tolerant mode.
func (r Registry) Validate(name string, data []byte) error {
	return r.ValidateMode(name, data, Tolerant)
}

// ValidateMode checks data against the named schema in the given mode.
func (r Registry) ValidateMode(name string, data []byte, mode Mode) error {
	s, ok := r[name]
	if !ok {
		return fmt.Errorf("unknown schema %s", name)
	}
	return r.ValidateSchemaMode(s, data, mode)
}

// ValidateSchema checks data against an inline schema in tolerant mode.
func (r Registry) ValidateSchema(s Schema, data []byte) error {
	return r.ValidateSchemaMode(s, data, Tolerant)
}

// ValidateSchemaMode checks data against an inline schema in the given mode.
func (r Registry) ValidateSchemaMode(s Schema, data []byte, mode Mode) error {
	v, e := Parse(data)
	if e != nil {
		return e
	}
	if err := r.check(s, v, mode); err != nil {
		return err
	}
	return nil
}

// Parse reads exactly one JSON value, rejecting duplicate object names and
// keeping every number as json.Number.
func Parse(data []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, e := read(d)
	if e != nil {
		return nil, &DecodeError{Path: "$", Expected: "valid JSON", Value: excerpt(data), Err: e}
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, &DecodeError{Path: "$", Expected: "a single JSON value", Value: excerpt(data), Err: fmt.Errorf("unexpected trailing JSON")}
	}
	return v, nil
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

func (r Registry) resolve(s Schema) Schema {
	for {
		ref, ok := s["$ref"].(string)
		if !ok {
			return s
		}
		s = r[strings.TrimPrefix(ref, "#/components/schemas/")]
	}
}

func fail(expected string, v any) *DecodeError {
	return &DecodeError{Path: "$", Expected: expected, Value: valueExcerpt(v)}
}

// check validates v. Paths are built only while unwinding a failure.
func (r Registry) check(s Schema, v any, mode Mode) *DecodeError {
	s = r.resolve(s)
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
				if r.check(Schema(b.(map[string]any)), v, mode) == nil {
					return nil
				}
			}
			return fail("a value matching one "+k+" variant", v)
		}
	}
	if a, ok := s["allOf"].([]any); ok {
		// Each branch declares only part of the object, so undocumented
		// members are judged against the union of all branches.
		branchMode := mode
		branchMode.UnknownProperties = false
		for _, b := range a {
			if e := r.check(Schema(b.(map[string]any)), v, branchMode); e != nil {
				return e
			}
		}
		if m, ok := v.(map[string]any); ok && mode.UnknownProperties {
			declared, open := map[string]bool{}, false
			r.collectProperties(s, declared, &open)
			if !open {
				for k, x := range m {
					if !declared[k] {
						return (&DecodeError{Path: "$", Expected: "no undocumented property", Value: valueExcerpt(x)}).at(memberSegment(k))
					}
				}
			}
		}
	}
	if a, ok := s["enum"].([]any); ok && mode.Enums {
		found := false
		for _, x := range a {
			if equal(x, v) {
				found = true
				break
			}
		}
		if !found {
			return fail("a known enum value", v)
		}
	}
	switch s["type"] {
	case "null":
		if v != nil {
			return fail("null", v)
		}
	case "string":
		x, ok := v.(string)
		if !ok {
			return fail("string", v)
		}
		if mode.Constraints {
			if n, ok := s["minLength"].(float64); ok && len([]rune(x)) < int(n) {
				return fail(fmt.Sprintf("string of at least %v characters", n), v)
			}
			if n, ok := s["maxLength"].(float64); ok && len([]rune(x)) > int(n) {
				return fail(fmt.Sprintf("string of at most %v characters", n), v)
			}
			if pattern, ok := s["pattern"].(string); ok {
				if re := compiled(pattern); re != nil && !re.MatchString(x) {
					return fail("string matching "+pattern, v)
				}
			}
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fail("boolean", v)
		}
	case "integer", "number":
		x, ok := v.(json.Number)
		if !ok {
			return fail(fmt.Sprint(s["type"]), v)
		}
		var q *big.Rat
		if s["type"] == "integer" {
			n, e := integerValue(x)
			if e != nil {
				return &DecodeError{Path: "$", Expected: "integer", Value: string(x), Err: e}
			}
			q = new(big.Rat).SetInt64(n)
		} else {
			if !mode.Constraints {
				return nil
			}
			if _, lo := s["minimum"]; !lo {
				if _, hi := s["maximum"]; !hi {
					return nil
				}
			}
			var valid bool
			q, valid = new(big.Rat).SetString(string(x))
			if !valid {
				return fail("number", v)
			}
		}
		if mode.Constraints {
			for _, k := range []string{"minimum", "maximum"} {
				if n, ok := s[k]; ok {
					b, ok := new(big.Rat).SetString(fmt.Sprint(n))
					if ok {
						cmp := q.Cmp(b)
						if k == "minimum" && cmp < 0 || k == "maximum" && cmp > 0 {
							return fail(fmt.Sprintf("%s %v", k, n), v)
						}
					}
				}
			}
		}
	case "array":
		a, ok := v.([]any)
		if !ok {
			return fail("array", v)
		}
		if mode.Constraints {
			if n, ok := s["minItems"].(float64); ok && len(a) < int(n) {
				return fail(fmt.Sprintf("at least %v items", n), v)
			}
			if n, ok := s["maxItems"].(float64); ok && len(a) > int(n) {
				return fail(fmt.Sprintf("at most %v items", n), v)
			}
			if s["uniqueItems"] == true {
				for i := range a {
					for j := 0; j < i; j++ {
						if reflect.DeepEqual(a[i], a[j]) {
							return fail("unique array items", v).at(fmt.Sprintf("[%d]", i))
						}
					}
				}
			}
		}
		if items, ok := s["items"].(map[string]any); ok {
			for i, x := range a {
				if e := r.check(Schema(items), x, mode); e != nil {
					return e.at(fmt.Sprintf("[%d]", i))
				}
			}
		}
	case "object":
		if _, ok := v.(map[string]any); !ok {
			return fail("object", v)
		}
	}
	if m, ok := v.(map[string]any); ok {
		if req, ok := s["required"].([]any); ok {
			for _, x := range req {
				if _, ok := m[x.(string)]; !ok {
					return &DecodeError{Path: "$", Expected: "required property " + x.(string), Value: "missing"}
				}
			}
		}
		if mode.Relaxed {
			if relaxed, ok := s[RelaxedKey].([]any); ok {
				for _, x := range relaxed {
					if _, ok := m[x.(string)]; !ok {
						return &DecodeError{Path: "$", Expected: "required property " + x.(string) + " (relaxed by spec-overrides.json)", Value: "missing"}
					}
				}
			}
		}
		props, _ := s["properties"].(map[string]any)
		for k, x := range m {
			if prop, ok := props[k].(map[string]any); ok {
				if e := r.check(Schema(prop), x, mode); e != nil {
					return e.at(memberSegment(k))
				}
			} else if add, ok := s["additionalProperties"].(map[string]any); ok {
				if e := r.check(Schema(add), x, mode); e != nil {
					return e.at(memberSegment(k))
				}
			} else if s["additionalProperties"] == false || mode.UnknownProperties && s["additionalProperties"] != true && props != nil && !listed(s, k) {
				return (&DecodeError{Path: "$", Expected: "no undocumented property", Value: valueExcerpt(x)}).at(memberSegment(k))
			}
		}
	}
	return nil
}

// listed reports whether k is a required or relaxed property name without a schema.
func listed(s Schema, k string) bool {
	for _, key := range []string{"required", RelaxedKey} {
		if names, ok := s[key].([]any); ok {
			for _, n := range names {
				if n == k {
					return true
				}
			}
		}
	}
	return false
}

// collectProperties gathers the properties declared by s and its allOf
// branches; open is set when any part allows additional properties.
func (r Registry) collectProperties(s Schema, declared map[string]bool, open *bool) {
	s = r.resolve(s)
	for k := range s {
		if k == "additionalProperties" && s[k] != false {
			*open = true
		}
	}
	if props, ok := s["properties"].(map[string]any); ok {
		for k := range props {
			declared[k] = true
		}
	}
	for _, key := range []string{"required", RelaxedKey} {
		if names, ok := s[key].([]any); ok {
			for _, n := range names {
				declared[n.(string)] = true
			}
		}
	}
	if a, ok := s["allOf"].([]any); ok {
		for _, b := range a {
			r.collectProperties(Schema(b.(map[string]any)), declared, open)
		}
	}
}

var patterns sync.Map

func compiled(pattern string) *regexp.Regexp {
	if v, ok := patterns.Load(pattern); ok {
		re, _ := v.(*regexp.Regexp)
		return re
	}
	re, e := regexp.Compile(pattern)
	if e != nil {
		re = nil
	}
	patterns.Store(pattern, re)
	return re
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

// Mask is a set of union variant indexes (0-based).
type Mask []uint64

// Has reports whether variant i (0-based) is in the set.
func (m Mask) Has(i int) bool { return i >= 0 && i/64 < len(m) && m[i/64]&(1<<uint(i%64)) != 0 }

// First returns the lowest 0-based variant index, or -1 for an empty set.
func (m Mask) First() int {
	for w, bitsWord := range m {
		if bitsWord != 0 {
			return w*64 + bits.TrailingZeros64(bitsWord)
		}
	}
	return -1
}

// Match decodes a union value: it returns the set of variants of the named
// oneOf/anyOf schema that accept data. Enum-aware matching is tried first so
// tagged branches stay distinguishable; tolerant matching follows so unknown
// enum values still select a variant (P1.1).
func (r Registry) Match(name string, data []byte) (Mask, error) {
	s, ok := r[name]
	if !ok {
		return nil, fmt.Errorf("unknown schema %s", name)
	}
	v, e := Parse(data)
	if e != nil {
		return nil, e
	}
	branches, _ := s["oneOf"].([]any)
	if branches == nil {
		branches, _ = s["anyOf"].([]any)
	}
	for _, mode := range []Mode{Discriminate, Tolerant} {
		mask := make(Mask, (len(branches)+63)/64)
		found := false
		for i, b := range branches {
			if r.check(Schema(b.(map[string]any)), v, mode) == nil {
				mask[i/64] |= 1 << uint(i%64)
				found = true
			}
		}
		if found {
			return mask, nil
		}
	}
	return nil, fail(fmt.Sprintf("a value matching one of the %d %s variants", len(branches), name), v)
}

// MatchBranch reports whether data tolerantly matches branch i of a union.
func (r Registry) MatchBranch(name string, i int, data []byte) error {
	s := r[name]
	branches, _ := s["oneOf"].([]any)
	if branches == nil {
		branches, _ = s["anyOf"].([]any)
	}
	if i < 0 || i >= len(branches) {
		return fmt.Errorf("%s has no variant %d", name, i+1)
	}
	v, e := Parse(data)
	if e != nil {
		return e
	}
	if err := r.check(Schema(branches[i].(map[string]any)), v, Tolerant); err != nil {
		return err
	}
	return nil
}
