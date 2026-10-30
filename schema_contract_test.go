package torn_test

// These expectations are derived directly from the pinned official JSON
// schemas. They do not invoke a decoder to manufacture the expected result.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp/syntax"
	"sort"
	"strings"
	"testing"

	"github.com/swervelord/Torn-Go-SDK/legacy"
	"github.com/swervelord/Torn-Go-SDK/models"
)

type schemaDoc struct {
	Components struct{ Schemas map[string]map[string]any }
}

func loadContract(t *testing.T, version string) schemaDoc {
	t.Helper()
	b, e := os.ReadFile("spec/openapi-" + version + ".json")
	if e != nil {
		t.Fatal(e)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var s schemaDoc
	if e = d.Decode(&s); e != nil {
		t.Fatal(e)
	}
	return s
}
func contractResolve(s map[string]any, defs map[string]map[string]any) map[string]any {
	if ref, ok := s["$ref"].(string); ok {
		return defs[strings.TrimPrefix(ref, "#/components/schemas/")]
	}
	return s
}
func contractNum(v any) float64 {
	if n, ok := v.(json.Number); ok {
		x, _ := n.Float64()
		return x
	}
	return 0
}
func contractSample(s map[string]any, defs map[string]map[string]any, variant, depth int) any {
	if depth > 40 {
		panic("unexpected recursive official schema")
	}
	s = contractResolve(s, defs)
	if e, ok := s["enum"].([]any); ok && len(e) > 0 {
		if variant == 2 {
			return e[len(e)-1]
		}
		return e[0]
	}
	if c, ok := s["const"]; ok {
		return c
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		if a, ok := s[key].([]any); ok && len(a) > 0 {
			index := 0
			if variant == 2 {
				index = len(a) - 1
			}
			return contractSample(a[index].(map[string]any), defs, variant, depth+1)
		}
	}
	if a, ok := s["allOf"].([]any); ok {
		merged := map[string]any{}
		for _, v := range a {
			x := contractSample(v.(map[string]any), defs, variant, depth+1)
			if m, ok := x.(map[string]any); ok {
				for k, v := range m {
					merged[k] = v
				}
			} else {
				return x
			}
		}
		return merged
	}
	typ, _ := s["type"].(string)
	if types, ok := s["type"].([]any); ok {
		typ = types[0].(string)
		if variant == 2 {
			for _, v := range types {
				if v == "null" {
					return nil
				}
			}
		}
		if typ == "null" && len(types) > 1 {
			typ = types[1].(string)
		}
	}
	if s["nullable"] == true && variant == 2 {
		return nil
	}
	if typ == "" {
		if _, ok := s["properties"]; ok {
			typ = "object"
		} else if _, ok := s["additionalProperties"]; ok {
			typ = "object"
		}
	}
	switch typ {
	case "object":
		out := map[string]any{}
		required := map[string]bool{}
		if r, ok := s["required"].([]any); ok {
			for _, v := range r {
				required[v.(string)] = true
			}
		}
		if props, ok := s["properties"].(map[string]any); ok {
			for k, v := range props {
				if required[k] || variant > 0 {
					out[k] = contractSample(v.(map[string]any), defs, variant, depth+1)
				}
			}
		}
		// A few official required lists name an otherwise unspecified property.
		// Its presence is required, while its value remains unconstrained.
		for key := range required {
			if _, exists := out[key]; !exists {
				out[key] = map[string]any{}
			}
		}
		if min := int(contractNum(s["minProperties"])); len(out) < min {
			if a, ok := s["additionalProperties"].(map[string]any); ok {
				for i := len(out); i < min; i++ {
					out[fmt.Sprint(i)] = contractSample(a, defs, variant, depth+1)
				}
			}
		}
		return out
	case "array":
		n := int(contractNum(s["minItems"]))
		if variant > 0 && n == 0 {
			n = 1
		}
		if max, ok := s["maxItems"]; ok && n > int(contractNum(max)) {
			n = int(contractNum(max))
		}
		out := make([]any, n)
		items, _ := s["items"].(map[string]any)
		for i := range out {
			out[i] = contractSample(items, defs, variant, depth+1)
		}
		return out
	case "integer", "number":
		n := float64(1)
		if min, ok := s["minimum"]; ok {
			n = contractNum(min)
		}
		if min, ok := s["exclusiveMinimum"]; ok {
			n = contractNum(min) + 1
		}
		if max, ok := s["maximum"]; ok && n > contractNum(max) {
			n = contractNum(max)
		}
		if variant == 2 {
			if max, ok := s["maximum"]; ok {
				n = contractNum(max)
			}
		}
		return json.Number(fmt.Sprintf("%.0f", n))
	case "string":
		v := "sample"
		if pattern, ok := s["pattern"].(string); ok {
			re, err := syntax.Parse(pattern, syntax.Perl)
			if err != nil {
				panic(err)
			}
			v = contractPatternSample(re)
		}
		if f, _ := s["format"].(string); f == "date-time" {
			v = "2026-10-23T00:00:00Z"
		}
		if min := int(contractNum(s["minLength"])); len(v) < min {
			v = strings.Repeat("a", min)
		}
		if max, ok := s["maxLength"]; ok && len(v) > int(contractNum(max)) {
			v = v[:int(contractNum(max))]
		}
		return v
	case "boolean":
		return variant > 0
	case "null":
		return nil
	default:
		return map[string]any{}
	}
}

func contractPatternSample(re *syntax.Regexp) string {
	switch re.Op {
	case syntax.OpLiteral:
		return string(re.Rune)
	case syntax.OpCharClass:
		return string(re.Rune[0])
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return "a"
	case syntax.OpConcat:
		var b strings.Builder
		for _, sub := range re.Sub {
			b.WriteString(contractPatternSample(sub))
		}
		return b.String()
	case syntax.OpAlternate, syntax.OpCapture:
		return contractPatternSample(re.Sub[0])
	case syntax.OpPlus:
		return contractPatternSample(re.Sub[0])
	case syntax.OpRepeat:
		return strings.Repeat(contractPatternSample(re.Sub[0]), re.Min)
	default:
		return ""
	}
}

func TestOfficialModelAcceptanceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		version  string
		validate func(string, []byte) error
		count    int
	}{{"6.13.6", legacy.Validate, 711}, {"6.15.0", models.Validate, 724}} {
		t.Run(tc.version, func(t *testing.T) {
			d := loadContract(t, tc.version)
			if len(d.Components.Schemas) != tc.count {
				t.Fatalf("schema inventory %d", len(d.Components.Schemas))
			}
			names := make([]string, 0, len(d.Components.Schemas))
			for n := range d.Components.Schemas {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, name := range names {
				for variant := 0; variant < 3; variant++ {
					t.Run(fmt.Sprintf("%s/%d", name, variant), func(t *testing.T) {
						sample := contractSample(d.Components.Schemas[name], d.Components.Schemas, variant, 0)
						raw, e := json.Marshal(sample)
						if e != nil {
							t.Fatal(e)
						}
						if e = tc.validate(name, raw); e != nil {
							t.Fatalf("documented sample rejected: %v; sample %.500s", e, raw)
						}
					})
				}
			}
		})
	}
}

func TestOfficialRequiredAndEnumRejection(t *testing.T) {
	d := loadContract(t, "6.15.0")
	checked := 0
	for name, s := range d.Components.Schemas {
		s = contractResolve(s, d.Components.Schemas)
		if req, ok := s["required"].([]any); ok && len(req) > 0 {
			sample, ok := contractSample(s, d.Components.Schemas, 0, 0).(map[string]any)
			if !ok {
				continue
			}
			delete(sample, req[0].(string))
			raw, _ := json.Marshal(sample)
			t.Run("missing/"+name, func(t *testing.T) {
				if e := models.Validate(name, raw); e == nil {
					t.Fatalf("missing required property %s accepted", req[0])
				}
			})
			checked++
		}
		if _, ok := s["enum"].([]any); ok {
			t.Run("enum/"+name, func(t *testing.T) {
				if e := models.Validate(name, []byte(`"__invalid_contract_enum__"`)); e == nil {
					t.Fatal("unknown closed enum value accepted")
				}
			})
			checked++
		}
	}
	if checked < 300 {
		t.Fatalf("insufficient negative boundary inventory: %d", checked)
	}
}

func TestSchemaDecodeRejectsMalformedNumbersAndTrailingJSON(t *testing.T) {
	for _, raw := range []string{`1.5`, `9223372036854775808`, `-9223372036854775809`, `1 {}`, `01`, `NaN`} {
		t.Run(raw, func(t *testing.T) {
			if err := models.Validate("UserId", []byte(raw)); err == nil {
				t.Fatalf("invalid integer accepted: %s", raw)
			}
		})
	}
}
