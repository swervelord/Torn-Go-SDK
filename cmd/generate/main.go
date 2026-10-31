// Command generate emits the pinned Torn API model and operation surface. It never uses the network.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type S = map[string]any
type generator struct {
	schemas     map[string]S
	definitions map[string]S
	order       []string
	pkg         string
	prefix      string
}

func keys[M ~map[string]V, V any](m M) []string {
	a := make([]string, 0, len(m))
	for k := range m {
		a = append(a, k)
	}
	sort.Strings(a)
	return a
}
func obj(v any) S {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return S{}
}
func str(v any) string { x, _ := v.(string); return x }
func name(s string) string {
	var b strings.Builder
	up := true
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			up = true
			continue
		}
		if up {
			r = unicode.ToUpper(r)
			up = false
		}
		b.WriteRune(r)
	}
	v := b.String()
	if v == "" {
		v = "Value"
	}
	if unicode.IsDigit([]rune(v)[0]) {
		v = "N" + v
	}
	switch v {
	case "Id":
		v = "ID"
	case "Url":
		v = "URL"
	case "Api":
		v = "API"
	case "Csv":
		v = "CSV"
	}
	return v
}
func (g *generator) add(n string, s S) string {
	if previous, ok := g.definitions[n]; ok {
		left, _ := json.Marshal(previous)
		right, _ := json.Marshal(s)
		if bytes.Equal(left, right) {
			return n
		}
		return g.add(n+"Field", s)
	}
	g.definitions[n] = s
	g.order = append(g.order, n)
	return n
}
func (g *generator) flatten(s S, seen map[string]bool) S {
	if ref := str(s["$ref"]); ref != "" {
		n := strings.TrimPrefix(ref, "#/components/schemas/")
		if seen[n] {
			return S{}
		}
		seen[n] = true
		return g.flatten(g.schemas[n], seen)
	}
	if a, ok := s["allOf"].([]any); ok {
		out := S{"type": "object", "properties": S{}, "required": []any{}}
		for _, v := range a {
			m := g.flatten(obj(v), seen)
			for k, p := range obj(m["properties"]) {
				obj(out["properties"])[k] = p
			}
			if r, ok := m["required"].([]any); ok {
				out["required"] = append(out["required"].([]any), r...)
			}
		}
		return out
	}
	return s
}
func (g *generator) typ(s S, n string) string {
	if ref := str(s["$ref"]); ref != "" {
		return g.prefix + name(strings.TrimPrefix(ref, "#/components/schemas/"))
	}
	if s["oneOf"] != nil || s["anyOf"] != nil || s["allOf"] != nil || s["enum"] != nil || len(obj(s["properties"])) > 0 {
		return g.prefix + g.add(n, s)
	}
	switch s["type"] {
	case "string":
		return "string"
	case "boolean":
		return "bool"
	case "integer":
		return "int64"
	case "number":
		return "json.Number"
	case "array":
		return "[]" + g.typ(obj(s["items"]), n+"Item")
	case "object":
		if m, ok := s["additionalProperties"].(map[string]any); ok {
			return "map[string]" + g.typ(m, n+"Value")
		}
		return "map[string]json.RawMessage"
	case "null":
		return "json.RawMessage"
	}
	return "json.RawMessage"
}

// scalarKind reports the primitive Go kind behind a generated named scalar
// type ("string", "int64", "bool", "json.Number"), or "" for other shapes.
func (g *generator) scalarKind(t string) string {
	switch t {
	case "string", "int64", "bool", "json.Number":
		return t
	}
	if g.prefix != "" && !strings.HasPrefix(t, g.prefix) {
		return ""
	}
	n := strings.TrimPrefix(t, g.prefix)
	def, ok := g.definitions[n]
	if !ok {
		return ""
	}
	s := g.flatten(def, map[string]bool{})
	if s["oneOf"] != nil || s["anyOf"] != nil || s["allOf"] != nil || len(obj(s["properties"])) > 0 || s["required"] != nil {
		return ""
	}
	switch s["type"] {
	case "string":
		return "string"
	case "integer":
		return "int64"
	case "boolean":
		return "bool"
	case "number":
		return "json.Number"
	}
	return ""
}

var scalarDecoder = map[string]string{"string": "contract.DecodeString", "int64": "contract.DecodeInt", "bool": "contract.DecodeBool", "json.Number": "contract.DecodeNumber"}

// decodeCall returns an expression decoding raw into target of Go type t.
func (g *generator) decodeCall(t, target string) string {
	if t == "json.RawMessage" {
		return "func() error {" + target + "=append(json.RawMessage(nil),raw...);return nil}()"
	}
	if k := g.scalarKind(t); k != "" {
		if k == t {
			return scalarDecoder[k] + "(raw,&" + target + ")"
		}
		return scalarDecoder[k] + "(raw,(*" + k + ")(&" + target + "))"
	}
	return "contract.DecodeInto(raw,&" + target + ")"
}

// branchName names a union branch for WhichName.
func (g *generator) branchName(u S, t string) string {
	if ref := str(u["$ref"]); ref != "" {
		return name(strings.TrimPrefix(ref, "#/components/schemas/"))
	}
	if u["type"] == "null" {
		return "null"
	}
	return t
}
func (g *generator) emit(n string, b *bytes.Buffer) {
	original := g.definitions[n]
	s := g.flatten(original, map[string]bool{})
	fmt.Fprintf(b, "// %s is generated from the pinned official Torn contract.\n", n)
	union, ok := s["oneOf"].([]any)
	if !ok {
		union, ok = s["anyOf"].([]any)
	}
	if ok {
		fmt.Fprintf(b, "// Which reports the matching variant and AsVariantN decodes it.\ntype %s struct { raw json.RawMessage; match contract.Mask }\n", n)
		fmt.Fprintf(b, "// UnmarshalJSON keeps the exact JSON and records every variant that accepts it.\nfunc(v *%s) UnmarshalJSON(data []byte) error {match,err:=registry.Match(%q,data);if err!=nil{return err};v.raw=append(v.raw[:0],data...);v.match=match;return nil}\n", n, n)
		fmt.Fprintf(b, "// MarshalJSON returns the exact JSON that was decoded or constructed.\nfunc(v %s) MarshalJSON()([]byte,error){if len(v.raw)==0{return []byte(\"null\"),nil};return v.raw,nil}\n", n)
		fmt.Fprintf(b, "// RawJSON returns a copy of the exact JSON value.\nfunc(v %s) RawJSON()json.RawMessage{return append(json.RawMessage(nil),v.raw...)}\n", n)
		fmt.Fprintf(b, "// Which returns the 1-based number of the first matching variant (the AsVariantN to call), or 0 for an empty value.\nfunc(v %s) Which()int{return v.match.First()+1}\n", n)
		fmt.Fprintf(b, "// Matches reports whether variant i (1-based) accepts the value; overlapping variants can all match.\nfunc(v %s) Matches(i int)bool{return v.match.Has(i-1)}\n", n)
		var names []string
		for i, u := range union {
			variant := n + fmt.Sprintf("Variant%d", i+1)
			t := g.typ(obj(u), variant)
			bn := g.branchName(obj(u), t)
			names = append(names, fmt.Sprintf("%q", bn))
			fmt.Fprintf(b, "// AsVariant%d decodes variant %d (%s); it fails when Matches(%d) is false.\nfunc(v %s) AsVariant%d()(%s,error){var out %s;if !v.match.Has(%d){return out,contract.NotVariant(%q,%d,v.raw)};raw:=v.raw;err:=%s;return out,err}\n", i+1, i+1, bn, i+1, n, i+1, t, t, i, n, i+1, g.decodeCall(t, "out"))
			fmt.Fprintf(b, "// New%sVariant%d builds a %s from variant %d (%s); it fails when the value would decode as another variant.\nfunc New%sVariant%d(value %s)(%s,error){var out %s;data,err:=json.Marshal(value);if err==nil{err=out.UnmarshalJSON(data)};if err==nil&&!out.match.Has(%d){err=contract.NotVariant(%q,%d,data)};return out,err}\n", n, i+1, n, i+1, bn, n, i+1, t, n, n, i, n, i+1)
		}
		fmt.Fprintf(b, "var %sVariantNames=[...]string{%s}\n", lower(n), strings.Join(names, ","))
		fmt.Fprintf(b, "// WhichName returns the schema or Go type name of the variant reported by Which (\"null\" for JSON null), or \"\" for an empty value.\nfunc(v %s) WhichName()string{if i:=v.Which();i>0{return %sVariantNames[i-1]};return \"\"}\n", n, lower(n))
		return
	}
	props := S{}
	for k, v := range obj(s["properties"]) {
		props[k] = v
	}
	if a, ok := s["required"].([]any); ok {
		for _, k := range a {
			if _, exists := props[k.(string)]; !exists {
				props[k.(string)] = S{}
			}
		}
	}
	relaxed := map[string]bool{}
	if a, ok := s[relaxedKey].([]any); ok {
		for _, k := range a {
			relaxed[k.(string)] = true
			if _, exists := props[k.(string)]; !exists {
				props[k.(string)] = S{}
			}
		}
	}
	if len(props) > 0 {
		req := map[string]bool{}
		if a, ok := s["required"].([]any); ok {
			for _, x := range a {
				req[x.(string)] = true
			}
		}
		fmt.Fprintf(b, "type %s struct {\n", n)
		types := map[string]string{}
		for _, k := range keys(props) {
			p := obj(props[k])
			t := g.typ(p, n+name(k))
			if relaxed[k] {
				// P1.2: a spec override proved Torn omits this property. A
				// json.RawMessage is nil when absent; other types become Optional.
				fmt.Fprintf(b, "// %s may be absent although the official contract requires it (spec/shared/spec-overrides.json).\n", name(k))
				if t != "json.RawMessage" {
					t = "Optional[" + t + "]"
				}
			} else if !req[k] {
				t = "Optional[" + t + "]"
			}
			types[k] = t
			fmt.Fprintf(b, "%s %s `json:%q`\n", name(k), t, k)
		}
		fmt.Fprintln(b, "// AdditionalProperties keeps undocumented members byte-for-byte.\nAdditionalProperties map[string]json.RawMessage `json:\"-\"`\n}")
		fmt.Fprintf(b, "// UnmarshalJSON decodes tolerantly: undocumented members are kept and enum values are open.\nfunc(v *%s) UnmarshalJSON(data []byte)error{fields,err:=contract.SplitObject(data);if err!=nil{return err};type plain %s;var out plain;", n, n)
		for _, k := range keys(props) {
			fmt.Fprintf(b, "if raw,ok:=fields[%q];ok{if err:=%s;err!=nil{return contract.At(err,%q)};delete(fields,%q)}", k, g.decodeCall(types[k], "out."+name(k)), k, k)
			if req[k] && !relaxed[k] {
				fmt.Fprintf(b, "else{return contract.Missing(%q)}", k)
			}
			fmt.Fprint(b, ";")
		}
		fmt.Fprintln(b, "out.AdditionalProperties=fields;*v="+n+"(out);return nil}")
		fmt.Fprintf(b, "// MarshalJSON writes documented and additional members.\nfunc(v %s) MarshalJSON()([]byte,error){fields:=make(map[string]json.RawMessage,len(v.AdditionalProperties)+%d);for k,x:=range v.AdditionalProperties{fields[k]=x};var data []byte;var err error;", n, len(props))
		for _, k := range keys(props) {
			f := "v." + name(k)
			optional := strings.HasPrefix(types[k], "Optional[")
			if optional {
				fmt.Fprintf(b, "if %s.Present{", f)
			} else if relaxed[k] {
				fmt.Fprintf(b, "if %s!=nil{", f)
			}
			fmt.Fprintf(b, "data,err=json.Marshal(%s);if err!=nil{return nil,err};fields[%q]=data;", f, k)
			if optional || relaxed[k] {
				fmt.Fprint(b, "};")
			}
		}
		fmt.Fprintln(b, "return json.Marshal(fields)}")
		return
	}
	// Scalars, arrays and maps remain concrete named Go types.
	base := S{}
	for k, v := range s {
		if k != "enum" {
			base[k] = v
		}
	}
	t := g.typ(base, n+"Value")
	if t == n {
		panic("self type " + n)
	}
	fmt.Fprintf(b, "type %s %s\n", n, t)
	if k := g.scalarKind(t); k != "" && k == t {
		fmt.Fprintf(b, "// UnmarshalJSON decodes the %s without enum or constraint checks (tolerant mode).\nfunc(v *%s) UnmarshalJSON(data []byte)error{return %s(data,(*%s)(v))}\n", k, n, scalarDecoder[k], k)
	} else {
		fmt.Fprintf(b, "// UnmarshalJSON decodes the value with exact integers.\nfunc(v *%s) UnmarshalJSON(data []byte)error{type plain %s;return contract.DecodeInto(data,(*plain)(v))}\n", n, n)
	}
	if a, ok := s["enum"].([]any); ok && (t == "string" || t == "int64" || t == "bool") {
		var consts, lines []string
		for i, x := range a {
			literal, _ := json.Marshal(x)
			cn := n + name(fmt.Sprint(x))
			if x == nil {
				continue
			}
			if _, ok := x.(string); !ok {
				cn = n + fmt.Sprintf("Value%d", i+1)
			}
			lines = append(lines, fmt.Sprintf("%s %s = %s\n", cn, n, string(literal)))
			consts = append(consts, cn)
		}
		if len(consts) > 0 {
			fmt.Fprintf(b, "// Known %s values. Other values decode too; see IsKnown.\nconst (\n%s)\n", n, strings.Join(lines, ""))
			if _, clash := g.definitions[n+"Values"]; clash {
				panic("enum values function clashes with schema " + n + "Values")
			}
			fmt.Fprintf(b, "// IsKnown reports whether v is a value of the pinned contract. Unknown values decode successfully and round-trip unchanged (P1.1).\nfunc(v %s) IsKnown()bool{switch v{case %s:return true};return false}\n", n, strings.Join(consts, ","))
			fmt.Fprintf(b, "// %sValues returns every known %s value in contract order.\nfunc %sValues()[]%s{return []%s{%s}}\n", n, n, n, n, n, strings.Join(consts, ","))
		}
	}
}
func lower(s string) string { return strings.ToLower(s[:1]) + s[1:] }
func write(path string, data []byte, check bool) {
	if strings.HasSuffix(path, ".go") {
		var err error
		data, err = format.Source(data)
		if err != nil {
			_ = os.WriteFile(path+".broken", data, 0644)
			panic(fmt.Errorf("format %s: %w", path, err))
		}
	}
	if check {
		old, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(old, data) {
			panic("generated output stale: " + path)
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		panic(err)
	}
}
func load(path string) S {
	raw, e := os.ReadFile(path)
	if e != nil {
		panic(e)
	}
	var s S
	if e = json.Unmarshal(raw, &s); e != nil {
		panic(e)
	}
	return s
}
func models(spec S, pkg string, check bool) *generator {
	schemas := map[string]S{}
	for k, v := range obj(obj(spec["components"])["schemas"]) {
		schemas[k] = obj(v)
	}
	if pkg == "models" {
		applyOverrides(schemas, "spec/shared/spec-overrides.json")
	}
	g := &generator{schemas: schemas, definitions: map[string]S{}, pkg: pkg}
	for _, n := range keys(schemas) {
		g.add(name(n), schemas[n])
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by go run ./cmd/generate; DO NOT EDIT.\npackage %s\nimport(\"encoding/json\";\"github.com/swervelord/Torn-Go-SDK/internal/contract\")\nvar _ = contract.Schema{}\n", pkg)
	for i := 0; i < len(g.order); i++ {
		g.emit(g.order[i], &b)
	}
	if pkg == "legacy" {
		write(pkg+"/types_generated.go", b.Bytes(), check)
		raw, _ := json.Marshal(g.definitions)
		write(pkg+"/schemas_generated.json", raw, check)
	}
	write(pkg+"/support.go", []byte(fmt.Sprintf(support, pkg)), check)
	emitDecoders(g, check)
	return g
}

const support = `// Code generated by go run ./cmd/generate; DO NOT EDIT.
package %s
import("bytes";_ "embed";"encoding/json";"sort";"github.com/swervelord/Torn-Go-SDK/internal/contract")
//go:embed schemas_generated.json
var schemaData []byte
var registry=contract.Load(schemaData)
// Optional distinguishes omitted, explicit null, and concrete values.
type Optional[T any] struct{Value T;Present bool;Null bool}
// Some returns a present, non-null Optional.
func Some[T any](value T)Optional[T]{return Optional[T]{Value:value,Present:true}}
// Null returns a present Optional holding JSON null.
func Null[T any]()Optional[T]{return Optional[T]{Present:true,Null:true}}
// UnmarshalJSON marks the value present and decodes it with exact integers.
func(v *Optional[T])UnmarshalJSON(data []byte)error{v.Present=true;v.Null=bytes.Equal(bytes.TrimSpace(data),[]byte("null"));if v.Null{var zero T;v.Value=zero;return nil};return contract.DecodeInto(data,&v.Value)}
// MarshalJSON writes the value, or null when absent or null.
func(v Optional[T])MarshalJSON()([]byte,error){if !v.Present||v.Null{return []byte("null"),nil};return json.Marshal(v.Value)}
// DecodeError locates a value that does not fit a model: JSON path, expected
// type or rule, and a truncated excerpt of the offending value (P1.4).
type DecodeError = contract.DecodeError
// Validate checks data against a pinned schema with the same tolerant rules as
// decoding: JSON types, required properties and union alternatives. Unknown
// enum values and undocumented properties are accepted (P1.3).
func Validate(name string,data []byte)error{return registry.Validate(name,data)}
// ValidateStrict additionally reports unknown enum values, undocumented
// properties, schema constraints and absent properties that
// spec/shared/spec-overrides.json relaxed (P1.3 strict mode).
func ValidateStrict(name string,data []byte)error{return registry.ValidateMode(name,data,contract.Strict)}
// ValidateSchema checks data against an inline schema (tolerant rules).
func ValidateSchema(schema,data []byte)error{var s contract.Schema;if err:=json.Unmarshal(schema,&s);err!=nil{return err};return registry.ValidateSchema(s,data)}
// CompiledSchema is an inline schema parsed once and resolved against this package's registry.
type CompiledSchema struct{s contract.Schema}
// CompileSchema parses an inline schema once; it panics on malformed schema JSON.
func CompileSchema(schema string)CompiledSchema{var s contract.Schema;if err:=json.Unmarshal([]byte(schema),&s);err!=nil{panic(err)};return CompiledSchema{s}}
// ValidateParams checks request parameters: types, required names and
// constraints; enum values are forwarded so new upstream values keep working.
func(c CompiledSchema)ValidateParams(data []byte)error{return registry.ValidateSchemaMode(c.s,data,contract.Params)}
// SchemaNames lists every component and named inline schema.
func SchemaNames()[]string{out:=make([]string,0,len(registry));for n:=range registry{out=append(out,n)};sort.Strings(out);return out}
// Schema returns the pinned JSON schema for name, or nil when unknown.
func Schema(name string)json.RawMessage{v,ok:=registry[name];if !ok{return nil};b,_:=json.Marshal(v);return b}
`

// relaxedKey mirrors contract.RelaxedKey; it marks override-relaxed properties in schemas_generated.json.
const relaxedKey = "x-sdk-relaxed-required"

// applyOverrides applies spec/shared/spec-overrides.json (PARITY_SPEC P1.2):
// each "optional" entry removes the property from its schema's required list
// and records it under relaxedKey so strict validation still reports it.
func applyOverrides(schemas map[string]S, path string) {
	var doc struct {
		Overrides []struct{ Schema, Property, Change string }
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	if err = json.Unmarshal(raw, &doc); err != nil {
		panic(err)
	}
	for _, o := range doc.Overrides {
		s, ok := schemas[o.Schema]
		if !ok {
			panic("spec override names unknown schema " + o.Schema)
		}
		if o.Change != "optional" {
			panic("unsupported spec override change " + o.Change)
		}
		req, _ := s["required"].([]any)
		kept := []any{}
		found := false
		for _, r := range req {
			if r == o.Property {
				found = true
				continue
			}
			kept = append(kept, r)
		}
		if !found {
			panic("spec override property is not required: " + o.Schema + "." + o.Property)
		}
		s["required"] = kept
		relaxed, _ := s[relaxedKey].([]any)
		s[relaxedKey] = append(relaxed, o.Property)
	}
}

func main() {
	check := flag.Bool("check", false, "verify generated files without writes")
	flag.Parse()
	current := load("spec/openapi-6.15.0.json")
	frozen := load("spec/openapi-6.13.6.json")
	g := models(current, "models", *check)
	models(frozen, "legacy", *check)
	operations(current, g, *check)
	errorCodes(*check)
}
