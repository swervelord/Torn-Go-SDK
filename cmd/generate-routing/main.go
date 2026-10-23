// Command generate-routing derives selection routes from pinned official OpenAPI.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type obj = map[string]any

var compatibilityTypes map[string]string

type endpoint struct {
	ID         string   `json:"id"`
	Path       string   `json:"path"`
	Parameters []string `json:"parameters"`
	Required   []string `json:"required"`
	Schema     string   `json:"schema"`
}
type capability struct {
	Resource   string     `json:"resource"`
	Name       string     `json:"name"`
	Generic    bool       `json:"generic"`
	Standalone bool       `json:"standalone"`
	Legacy     bool       `json:"legacy"`
	CSV        bool       `json:"csv"`
	Endpoints  []endpoint `json:"endpoints"`
}

func m(v any) obj {
	if x, ok := v.(map[string]any); ok {
		return x
	}
	return obj{}
}
func a(v any) []any {
	if x, ok := v.([]any); ok {
		return x
	}
	return nil
}
func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
func refs(v any, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		if r := str(x["$ref"]); r != "" {
			out[strings.TrimPrefix(r, "#/components/schemas/")] = true
		}
		for _, v := range x {
			refs(v, out)
		}
	case []any:
		for _, v := range x {
			refs(v, out)
		}
	}
}
func names(v any, out map[string]bool) {
	x := m(v)
	for _, e := range a(x["enum"]) {
		out[str(e)] = true
	}
	for _, k := range []string{"oneOf", "anyOf"} {
		for _, v := range a(x[k]) {
			names(v, out)
		}
	}
}
func exported(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}
func main() {
	root := "."
	check := false
	for _, arg := range os.Args[1:] {
		if arg == "-check" {
			check = true
		} else {
			root = arg
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "spec", "openapi-6.13.8.json"))
	must(err)
	var spec obj
	must(json.Unmarshal(data, &spec))
	var overrides struct {
		Aliases       map[string]string   `json:"generic_only_responses"`
		Legacy        map[string][]string `json:"legacy_selections"`
		Compatibility map[string]string   `json:"compatibility_types"`
	}
	data, err = os.ReadFile(filepath.Join(root, "spec", "routing-overrides.json"))
	must(err)
	must(json.Unmarshal(data, &overrides))
	compatibilityTypes = overrides.Compatibility
	var v1 struct {
		Resources map[string][]string `json:"resources"`
		Filters   []string            `json:"filters"`
		Evidence  string              `json:"evidence"`
	}
	data, err = os.ReadFile(filepath.Join(root, "spec", "v1-catalog.json"))
	must(err)
	must(json.Unmarshal(data, &v1))
	paths := m(spec["paths"])
	schemas := m(m(spec["components"])["schemas"])
	parameters := m(m(spec["components"])["parameters"])
	var catalog []capability
	generic := map[string]endpoint{}
	resources := []string{"company", "faction", "forum", "key", "market", "property", "racing", "torn", "user"}
	for _, r := range resources {
		op := m(m(paths["/"+r])["get"])
		ep := readEndpoint("/"+r, op, parameters)
		if ep.ID == "" {
			ep.ID = "get" + exported(r) + "Generic"
		}
		generic[r] = ep
		sel := map[string]bool{}
		names(schemas[exported(r)+"SelectionName"], sel)
		byName := map[string][]endpoint{}
		standalone := map[string]bool{}
		csv := map[string]bool{}
		genericRefs := map[string]bool{}
		refs(m(op["responses"])["200"], genericRefs)
		inEnum := map[string]bool{}
		for n := range sel {
			inEnum[n] = true
		}
		for _, n := range overrides.Legacy[r] {
			sel[n] = true
		}
		for path, v := range paths {
			if !strings.HasPrefix(path, "/"+r+"/") {
				continue
			}
			parts := strings.Split(path, "/")
			name := strings.ToLower(parts[len(parts)-1])
			if strings.HasPrefix(name, "{") {
				continue
			}
			o := m(m(v)["get"])
			if len(o) == 0 {
				continue
			}
			ep := readEndpoint(path, o, parameters)
			sel[name] = true
			byName[name] = append(byName[name], ep)
			if strings.Contains(strings.ToLower(str(o["description"])), "standalone") {
				standalone[name] = true
			}
			content := m(m(m(o["responses"])["200"])["content"])
			if _, ok := content["text/csv"]; ok {
				csv[name] = true
			}
		}
		for key, schema := range overrides.Aliases {
			if strings.HasPrefix(key, r+"/") {
				name := strings.TrimPrefix(key, r+"/")
				x := ep
				x.Schema = schema
				byName[name] = []endpoint{x}
			}
		}
		ordered := []string{}
		for name := range sel {
			ordered = append(ordered, name)
		}
		sort.Strings(ordered)
		for _, name := range ordered {
			eps := byName[name]
			sort.Slice(eps, func(i, j int) bool { return eps[i].Path < eps[j].Path })
			can := false
			for _, e := range eps {
				if genericRefs[e.Schema] && inEnum[name] {
					can = true
				}
			}
			legacy := len(eps) == 0
			catalog = append(catalog, capability{r, name, can && !standalone[name], standalone[name], legacy, csv[name], eps})
		}
	}
	var out bytes.Buffer
	fmt.Fprintln(&out, "// Code generated by go run ./cmd/generate-routing; DO NOT EDIT.\npackage torn\nimport (\"context\";\"encoding/json\"; \"fmt\";\"github.com/swervelord/Torn-Go-SDK/models\";\"github.com/swervelord/Torn-Go-SDK/compat\")")
	catJSON, _ := json.Marshal(catalog)
	genJSON, _ := json.Marshal(generic)
	type legacyEntry struct {
		Resource   string `json:"resource"`
		Name       string `json:"name"`
		LegacyOnly bool   `json:"legacy_only"`
	}
	var legacyCatalog []legacyEntry
	for _, resource := range resources {
		selectionNames := append([]string(nil), v1.Resources[resource]...)
		sort.Strings(selectionNames)
		for _, name := range selectionNames {
			only := true
			for _, c := range catalog {
				if c.Resource == resource && c.Name == name && !c.Legacy {
					only = false
				}
			}
			legacyCatalog = append(legacyCatalog, legacyEntry{resource, name, only})
		}
	}
	legacyJSON, _ := json.Marshal(legacyCatalog)
	filterJSON, _ := json.Marshal(v1.Filters)
	fmt.Fprintf(&out, "var legacySelectionCatalog = func() []LegacySelectionCapability{var v []LegacySelectionCapability;if e:=json.Unmarshal([]byte(%q),&v);e!=nil{panic(e)};return v}()\n", legacyJSON)
	fmt.Fprintf(&out, "var legacyAllowedFilters = func() []string{var v []string;if e:=json.Unmarshal([]byte(%q),&v);e!=nil{panic(e)};return v}()\n", filterJSON)
	fmt.Fprintf(&out, "var selectionCatalog = func() []SelectionCapability {var v []SelectionCapability; if e:=json.Unmarshal([]byte(%q),&v);e!=nil{panic(e)};return v}()\n", catJSON)
	fmt.Fprintf(&out, "var genericCatalog = func() map[string]SelectionEndpoint {var v map[string]SelectionEndpoint;if e:=json.Unmarshal([]byte(%q),&v);e!=nil{panic(e)};return v}()\n", genJSON)
	for _, r := range resources {
		fmt.Fprintf(&out, "type %sResource struct{}\n", exported(r))
	}
	for _, r := range resources {
		fmt.Fprintf(&out, "var %sSelections = struct {\n", exported(r))
		for _, c := range catalog {
			if c.Resource != r || c.CSV {
				continue
			}
			schema := typedSchema(c)
			if schema != "" {
				fmt.Fprintf(&out, "%s Selection[%sResource,%s]\n", exported(c.Name), exported(r), schema)
			}
		}
		fmt.Fprintln(&out, "}{")
		for _, c := range catalog {
			if c.Resource != r || c.CSV {
				continue
			}
			schema := typedSchema(c)
			if schema != "" {
				fmt.Fprintf(&out, "%s:Selection[%sResource,%s]{name:%q,resource:%q},\n", exported(c.Name), exported(r), schema, c.Name, r)
			}
		}
		fmt.Fprintln(&out, "}")
	}
	typed, raw := 0, 0
	for _, c := range catalog {
		if c.CSV {
			ep := c.Endpoints[0]
			n := exported(c.Resource) + exported(c.Name)
			fmt.Fprintf(&out, "func(c *Client) %s(ctx context.Context,params map[string]any,opts RequestOptions)(string,ExecutionReport,error){b,r,e:=c.DoOperation(ctx,Operation{ID:%q,Path:%q,Method:\"GET\",CSV:true},allowedParams(params,[]string{\"timestamp\",\"comment\"}),opts);var out string;if e==nil{e=json.Unmarshal(b,&out)};return out,r,e}\n", n, ep.ID, ep.Path)
			typed++
			continue
		}
		n := exported(c.Resource) + exported(c.Name)
		fmt.Fprintf(&out, "func(c *Client) Raw%s(ctx context.Context,params map[string]any,opts RequestOptions)(ExecutionResult,error){return c.RawSelections(ctx,%q,[]string{%q},params,opts)}\n", n, c.Resource, c.Name)
		raw++
		schema := typedSchema(c)
		if schema != "" {
			fmt.Fprintf(&out, "func(c *Client) %s(ctx context.Context,params map[string]any,opts RequestOptions)(%s,ExecutionReport,error){return Fetch(ctx,c,%sSelections.%s,params,opts)}\n", n, schema, exported(c.Resource), exported(c.Name))
			typed++
		}
	}
	fmt.Fprintln(&out, `func(c *Client) CompanyDetailed(ctx context.Context,params map[string]any,opts RequestOptions)(compat.CompanyDetailedBundle,ExecutionReport,error){return c.CompanyProfile(ctx,params,opts)}
func(c *Client) TornStock(ctx context.Context,params map[string]any,opts RequestOptions)(compat.TornStockBundle,ExecutionReport,error){var out compat.TornStockBundle;if _,ok:=parameterValue(params,"id");!ok{return out,ExecutionReport{},fmt.Errorf("stock requires id")};return DecodeSelections(ctx,c,"torn",[]string{"stocks"},params,opts,func(b json.RawMessage)(compat.TornStockBundle,error){var v compat.TornStockBundle;e:=json.Unmarshal(b,&v);return v,e})}`)
	typed += 2
	formatted, err := format.Source(out.Bytes())
	must(err)
	writeOutput(filepath.Join(root, "selections_generated.go"), formatted, check)
	manifest := obj{"source": "https://www.torn.com/swagger/openapi.json", "version": "6.13.8", "capabilities": catalog, "typed_helpers": typed, "raw_helpers": raw, "v1_catalog": legacyCatalog, "v1_evidence": v1.Evidence, "derivation": "Selection enums plus exact endpoint path tails; generic support requires response schema membership in generic anyOf. Standalone from endpoint description. CSV from content type. Legacy only for enum selections without exact v2 endpoint."}
	b, _ := json.MarshalIndent(manifest, "", "  ")
	writeOutput(filepath.Join(root, "spec", "routing-manifest.json"), append(b, '\n'), check)
	fmt.Printf("Generated %d selections, %d typed helpers, %d raw helpers\n", len(catalog), typed, raw)
}
func writeOutput(path string, data []byte, check bool) {
	if check {
		existing, e := os.ReadFile(path)
		must(e)
		if !bytes.Equal(existing, data) {
			must(fmt.Errorf("generated output stale: %s", path))
		}
		return
	}
	must(os.WriteFile(path, data, 0644))
}
func typedSchema(c capability) string {
	if name := compatibilityTypes[c.Resource+"/"+c.Name]; name != "" {
		return "compat." + name
	}
	schema := ""
	for _, e := range c.Endpoints {
		if e.Schema == "" {
			continue
		}
		if schema != "" && schema != e.Schema {
			return ""
		}
		schema = e.Schema
	}
	if schema == "" {
		return ""
	}
	return "models." + schema
}
func readEndpoint(path string, op obj, parameters obj) endpoint {
	ep := endpoint{ID: str(op["operationId"]), Path: path}
	for _, v := range a(op["parameters"]) {
		p := m(v)
		if ref := str(p["$ref"]); ref != "" {
			p = m(parameters[strings.TrimPrefix(ref, "#/components/parameters/")])
		}
		name := str(p["name"])
		if str(p["in"]) == "path" {
			ep.Required = append(ep.Required, name)
		} else if str(p["in"]) == "query" {
			ep.Parameters = append(ep.Parameters, name)
		}
	}
	r := m(m(m(m(op["responses"])["200"])["content"])["application/json"])
	ep.Schema = strings.TrimPrefix(str(m(r["schema"])["$ref"]), "#/components/schemas/")
	sort.Strings(ep.Parameters)
	sort.Strings(ep.Required)
	return ep
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
