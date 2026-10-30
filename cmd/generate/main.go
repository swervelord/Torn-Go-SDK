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
func (g *generator) emit(n string, b *bytes.Buffer) {
	original := g.definitions[n]
	s := g.flatten(original, map[string]bool{})
	fmt.Fprintf(b, "// %s is generated from the pinned official Torn contract.\n", n)
	union, ok := s["oneOf"].([]any)
	if !ok {
		union, ok = s["anyOf"].([]any)
	}
	if ok {
		fmt.Fprintf(b, "type %s struct { raw json.RawMessage }\n", n)
		fmt.Fprintf(b, "func(v *%s) UnmarshalJSON(data []byte) error {normalized,err:=registry.Normalize(%q,data);if err!=nil{return err};data=normalized;v.raw=append(v.raw[:0],data...);return nil}\nfunc(v %s) MarshalJSON()([]byte,error){if len(v.raw)==0{return []byte(\"null\"),nil};return v.raw,nil}\nfunc(v %s) RawJSON()json.RawMessage{return append(json.RawMessage(nil),v.raw...)}\n", n, n, n, n)
		for i, u := range union {
			variant := n + fmt.Sprintf("Variant%d", i+1)
			t := g.typ(obj(u), variant)
			raw, _ := json.Marshal(u)
			fmt.Fprintf(b, "func(v %s) AsVariant%d()(%s,error){var out %s;if err:=registry.ValidateSchema(contract.Schema(%s),v.raw);err!=nil{return out,err};err:=json.Unmarshal(v.raw,&out);return out,err}\n", n, i+1, t, t, schemaLiteral(raw))
			fmt.Fprintf(b, "func New%sVariant%d(value %s)(%s,error){var out %s;data,err:=json.Marshal(value);if err==nil{err=registry.ValidateSchema(contract.Schema(%s),data)};if err==nil{err=out.UnmarshalJSON(data)};return out,err}\n", n, i+1, t, n, n, schemaLiteral(raw))
		}
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
	if len(props) > 0 {
		req := map[string]bool{}
		if a, ok := s["required"].([]any); ok {
			for _, x := range a {
				req[x.(string)] = true
			}
		}
		fmt.Fprintf(b, "type %s struct {\n", n)
		for _, k := range keys(props) {
			p := obj(props[k])
			t := g.typ(p, n+name(k))
			if !req[k] {
				t = "Optional[" + t + "]"
			}
			fmt.Fprintf(b, "%s %s `json:%q`\n", name(k), t, k)
		}
		fmt.Fprintln(b, "AdditionalProperties map[string]json.RawMessage `json:\"-\"`\n}")
		fmt.Fprintf(b, "func(v *%s) UnmarshalJSON(data []byte)error{normalized,err:=registry.Normalize(%q,data);if err!=nil{return err};data=normalized;type plain %s;var out plain;var fields map[string]json.RawMessage;if err:=json.Unmarshal(data,&fields);err!=nil{return err};", n, n, n)
		for _, k := range keys(props) {
			fmt.Fprintf(b, "if raw,ok:=fields[%q];ok{if err:=json.Unmarshal(raw,&out.%s);err!=nil{return err}};delete(fields,%q);", k, name(k), k)
		}
		fmt.Fprintln(b, "out.AdditionalProperties=fields;*v="+n+"(out);return nil}")
		fmt.Fprintf(b, "func(v %s) MarshalJSON()([]byte,error){fields:=make(map[string]json.RawMessage,len(v.AdditionalProperties)+%d);for k,x:=range v.AdditionalProperties{fields[k]=x};var data []byte;var err error;", n, len(props))
		for _, k := range keys(props) {
			f := "v." + name(k)
			if !req[k] {
				fmt.Fprintf(b, "if %s.Present{", f)
			}
			fmt.Fprintf(b, "data,err=json.Marshal(%s);if err!=nil{return nil,err};fields[%q]=data;", f, k)
			if !req[k] {
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
	fmt.Fprintf(b, "func(v *%s) UnmarshalJSON(data []byte)error{normalized,err:=registry.Normalize(%q,data);if err!=nil{return err};data=normalized;type plain %s;var out plain;if err:=json.Unmarshal(data,&out);err!=nil{return err};*v=%s(out);return nil}\n", n, n, n, n)
	if a, ok := s["enum"].([]any); ok {
		for i, x := range a {
			literal, _ := json.Marshal(x)
			cn := n + name(fmt.Sprint(x))
			if x == nil {
				continue
			}
			if _, ok := x.(string); !ok {
				cn = n + fmt.Sprintf("Value%d", i+1)
			}
			if t == "string" || t == "int64" || t == "bool" {
				fmt.Fprintf(b, "const %s %s = %s\n", cn, n, string(literal))
			}
		}
	}
}
func schemaLiteral(raw []byte) string { return "decodeSchema(" + fmt.Sprintf("%q", string(raw)) + ")" }
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
func Some[T any](value T)Optional[T]{return Optional[T]{Value:value,Present:true}}
func Null[T any]()Optional[T]{return Optional[T]{Present:true,Null:true}}
func(v *Optional[T])UnmarshalJSON(data []byte)error{v.Present=true;v.Null=bytes.Equal(bytes.TrimSpace(data),[]byte("null"));if v.Null{var zero T;v.Value=zero;return nil};return json.Unmarshal(data,&v.Value)}
func(v Optional[T])MarshalJSON()([]byte,error){if !v.Present||v.Null{return []byte("null"),nil};return json.Marshal(v.Value)}
// Validate checks a pinned schema including required properties and union alternatives.
func Validate(name string,data []byte)error{return registry.Validate(name,data)}
func ValidateSchema(schema,data []byte)error{var s contract.Schema;if err:=json.Unmarshal(schema,&s);err!=nil{return err};return registry.ValidateSchema(s,data)}
func SchemaNames()[]string{out:=make([]string,0,len(registry));for n:=range registry{out=append(out,n)};sort.Strings(out);return out}
func Schema(name string)json.RawMessage{v,ok:=registry[name];if !ok{return nil};b,_:=json.Marshal(v);return b}
func decodeSchema(s string)map[string]any{var out map[string]any;if err:=json.Unmarshal([]byte(s),&out);err!=nil{panic(err)};return out}
`

func main() {
	check := flag.Bool("check", false, "verify generated files without writes")
	flag.Parse()
	current := load("spec/openapi-6.15.0.json")
	frozen := load("spec/openapi-6.13.6.json")
	g := models(current, "models", *check)
	models(frozen, "legacy", *check)
	operations(current, g, *check)
}
