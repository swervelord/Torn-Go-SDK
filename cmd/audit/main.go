// Command audit derives a reproducible typing report from committed contracts.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
)

type gap struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

func load(path string) map[string]any {
	b, e := os.ReadFile(path)
	if e != nil {
		panic(e)
	}
	var v map[string]any
	if e = json.Unmarshal(b, &v); e != nil {
		panic(e)
	}
	return v
}
func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func scan(v any, path string, gaps *[]gap) {
	switch x := v.(type) {
	case map[string]any:
		if req, ok := x["required"].([]any); ok {
			props, _ := x["properties"].(map[string]any)
			for _, key := range req {
				if _, exists := props[key.(string)]; !exists {
					*gaps = append(*gaps, gap{path + "/" + key.(string), "Official required property has no property schema; its value remains exact raw JSON."})
				}
			}
		}
		for _, k := range keys(x) {
			sub := x[k]
			switch k {
			case "properties":
				if props, ok := sub.(map[string]any); ok {
					for _, name := range keys(props) {
						p, _ := props[name].(map[string]any)
						constrained := false
						for _, c := range []string{"type", "$ref", "properties", "oneOf", "anyOf", "allOf", "enum", "const"} {
							if _, exists := p[c]; exists {
								constrained = true
							}
						}
						if !constrained {
							*gaps = append(*gaps, gap{path + "/properties/" + name, "Official property has no declared value type; preserved as exact raw JSON."})
						}
						scan(p, path+"/properties/"+name, gaps)
					}
				}
			case "items", "additionalProperties", "oneOf", "anyOf", "allOf", "not":
				scan(sub, path+"/"+k, gaps)
			}
		}
	case []any:
		for i, s := range x {
			scan(s, fmt.Sprintf("%s/%d", path, i), gaps)
		}
	}
}
func main() {
	check := flag.Bool("check", false, "verify the committed typing report")
	flag.Parse()
	var gaps []gap
	doc := load("spec/openapi-6.15.0.json")
	components := doc["components"].(map[string]any)
	for _, section := range []string{"schemas", "parameters"} {
		entries, _ := components[section].(map[string]any)
		for _, name := range keys(entries) {
			value := entries[name]
			path := "#/components/" + section + "/" + name
			if section == "parameters" {
				value = value.(map[string]any)["schema"]
				path += "/schema"
			}
			scan(value, path, &gaps)
		}
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].Path < gaps[j].Path })
	compat := load("spec/compatibility-models.json")
	var rawFields []string
	var walk func(any, string)
	walk = func(v any, path string) {
		switch x := v.(type) {
		case map[string]any:
			if x["name"] == "json" || x["name"] == "raw" || x["name"] == "value" {
				rawFields = append(rawFields, path)
			}
			for _, k := range keys(x) {
				walk(x[k], path+"/"+k)
			}
		case []any:
			for i, a := range x {
				walk(a, fmt.Sprintf("%s/%d", path, i))
			}
		}
	}
	walk(compat, "compatibility-models")
	sort.Strings(rawFields)
	report := map[string]any{"version": "6.15.0", "source": "https://www.torn.com/swagger/openapi.json", "official_unspecified_fields": gaps, "compatibility_raw_descriptors": rawFields, "compatibility_models": len(compat), "scope": "All audited official v2 schemas and operations; historical 6.13.6 schemas; documented selection routing. Unspecified v1 payloads and open log maps cannot be claimed fully statically typed.", "json_policy": "Signed 64-bit generated integers, exact json.Number for decimal values, raw JSON for unspecified values; missing/null presence wrappers; duplicate names rejected; open string variants accepted where documented enum-or-string unions overlap."}
	b, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		panic(e)
	}
	b = append(b, '\n')
	path := "spec/typing-report.json"
	if *check {
		old, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(old, b) {
			panic("typing report stale: run go run ./cmd/audit")
		}
	} else if e = os.WriteFile(path, b, 0644); e != nil {
		panic(e)
	}
	model := load("spec/model-coverage.json")
	frozen := load("spec/openapi-6.13.6.json")["components"].(map[string]any)["schemas"].(map[string]any)
	currentMap := model["schemas"].(map[string]any)
	legacyMap := map[string]string{}
	legacyFile, err := parser.ParseFile(token.NewFileSet(), "legacy/types_generated.go", nil, 0)
	if err != nil {
		panic(err)
	}
	legacyTypes := map[string]bool{}
	ast.Inspect(legacyFile, func(n ast.Node) bool {
		if t, ok := n.(*ast.TypeSpec); ok {
			legacyTypes[t.Name.Name] = true
		}
		return true
	})
	for _, n := range keys(frozen) {
		public, ok := currentMap[n].(string)
		if !ok {
			public = "models." + n
		}
		name := strings.TrimPrefix(public, "models.")
		if !legacyTypes[name] {
			panic("historical schema missing public mapping: " + n)
		}
		legacyMap[n] = "legacy." + name
	}
	compatMap := map[string]string{}
	for _, name := range keys(compat) {
		compatMap[name] = "compat." + name
	}
	routing := load("spec/routing-manifest.json")
	// Record literal Go API names and return types, including aliases and CSV,
	// so consumers do not need to infer names from a selection spelling.
	helpers := []map[string]any{}
	selectionFile, err := parser.ParseFile(token.NewFileSet(), "selections_generated.go", nil, 0)
	if err != nil {
		panic(err)
	}
	for _, decl := range selectionFile.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil {
			continue
		}
		entry := map[string]any{"public_api": "(*torn.Client)." + fn.Name.Name}
		var returns []string
		for _, field := range fn.Type.Results.List {
			var b bytes.Buffer
			if err := format.Node(&b, token.NewFileSet(), field.Type); err != nil {
				panic(err)
			}
			returns = append(returns, b.String())
		}
		entry["returns"] = returns
		for _, raw := range routing["capabilities"].([]any) {
			cap := raw.(map[string]any)
			resource := cap["resource"].(string)
			selection := cap["name"].(string)
			public := strings.ToUpper(resource[:1]) + resource[1:] + strings.ToUpper(selection[:1]) + selection[1:]
			if fn.Name.Name == public || fn.Name.Name == "Raw"+public {
				entry["resource"] = resource
				entry["selection"] = selection
				entry["csv"] = cap["csv"]
				break
			}
		}
		if fn.Name.Name == "CompanyDetailed" {
			entry["resource"] = "company"
			entry["selection"] = "profile"
			entry["alias"] = true
		}
		if fn.Name.Name == "TornStock" {
			entry["resource"] = "torn"
			entry["selection"] = "stocks"
			entry["alias"] = true
		}
		helpers = append(helpers, entry)
	}
	for _, raw := range model["operations"].([]any) {
		entry := raw.(map[string]any)
		entry["public_api"] = "(*torn.Client)." + strings.TrimSuffix(entry["params"].(string), "Params")
	}
	coverage := map[string]any{"current": model, "historical": map[string]any{"version": "6.13.6", "schemas": legacyMap}, "compatibility": compatMap, "routing": routing, "convenience_helpers": helpers, "limits": "Inventory coverage is distinct from code branch coverage and all possible live game states. See spec/typing-report.json for unspecified values."}
	covered, e := json.MarshalIndent(coverage, "", "  ")
	if e != nil {
		panic(e)
	}
	covered = append(covered, '\n')
	if *check {
		old, e := os.ReadFile("spec/coverage.json")
		if e != nil || !bytes.Equal(old, covered) {
			panic("coverage manifest stale: run go run ./cmd/audit")
		}
	} else if e = os.WriteFile("spec/coverage.json", covered, 0644); e != nil {
		panic(e)
	}
	fmt.Printf("Typing report: %d official underspecified properties, %d compatibility declarations.\n", len(gaps), len(compat))
}
