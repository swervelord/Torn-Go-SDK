package torn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// PlannedCall is an independently executable API call. FallbackCalls are used
// only when the caller explicitly enables runtime legacy fallback.
type PlannedCall struct {
	Operation     Operation
	Selections    []string
	Params        map[string]any
	Version       string
	Fallback      bool
	FallbackCalls []PlannedCall
}
type Plan struct{ Calls []PlannedCall }

// SelectionCapability records the documented routing surface. CSV endpoints
// are intentionally excluded from JSON selection batches.
type SelectionCapability struct {
	Resource   string              `json:"resource"`
	Name       string              `json:"name"`
	Generic    bool                `json:"generic"`
	Standalone bool                `json:"standalone"`
	Legacy     bool                `json:"legacy"`
	CSV        bool                `json:"csv"`
	Endpoints  []SelectionEndpoint `json:"endpoints"`
}
type SelectionEndpoint struct {
	ID         string   `json:"id"`
	Path       string   `json:"path"`
	Parameters []string `json:"parameters"`
	Required   []string `json:"required"`
	Schema     string   `json:"schema"`
}

// LegacySelectionCapability identifies the deliberately supported compatibility
// catalog. Migrated entries preserve SDK compatibility; this is not a complete
// authenticated API v1 wire-schema inventory.
type LegacySelectionCapability struct {
	Resource   string `json:"resource"`
	Name       string `json:"name"`
	LegacyOnly bool   `json:"legacy_only"`
}

func LegacySelections() []LegacySelectionCapability {
	return append([]LegacySelectionCapability(nil), legacySelectionCatalog...)
}
func supportsLegacy(resource, name string) bool {
	for _, c := range legacySelectionCatalog {
		if c.Resource == resource && c.Name == name {
			return true
		}
	}
	return false
}
func SupportsV1Selection(resource, name string) bool {
	return supportsLegacy(strings.ToLower(strings.TrimSpace(resource)), strings.ToLower(strings.TrimSpace(name)))
}
func V1Catalog() map[string][]string {
	out := map[string][]string{}
	for _, c := range legacySelectionCatalog {
		out[c.Resource] = append(out[c.Resource], c.Name)
	}
	return out
}
func AllowedV1Filters() []string { return append([]string(nil), legacyAllowedFilters...) }
func PlanV1Selections(resource string, selections []string, params map[string]any) (Plan, error) {
	return PlanLegacySelections(resource, selections, params)
}
func PlanLegacySelections(resource string, selections []string, params map[string]any) (Plan, error) {
	resource = strings.ToLower(strings.TrimSpace(resource))
	set := map[string]bool{}
	for _, name := range selections {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if !supportsLegacy(resource, name) {
			return Plan{}, fmt.Errorf("unsupported v1 selection %s/%s", resource, name)
		}
		set[name] = true
	}
	if len(set) == 0 {
		return Plan{}, fmt.Errorf("at least one supported v1 selection is required")
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return Plan{Calls: []PlannedCall{legacyCall(resource, names, params)}}, nil
}

// Capabilities returns independent copies so callers cannot mutate planning.
func Capabilities() []SelectionCapability {
	b, _ := json.Marshal(selectionCatalog)
	var result []SelectionCapability
	_ = json.Unmarshal(b, &result)
	return result
}

// PlanSelections combines selections sharing one resource, target, and filter
// map. Parameter values are never merged across independently supplied requests.
func PlanSelections(resource string, selections []string, params map[string]any) (Plan, error) {
	resource = strings.ToLower(strings.TrimSpace(resource))
	generic, ok := genericCatalog[resource]
	if !ok {
		return Plan{}, fmt.Errorf("unknown resource %q", resource)
	}
	names := map[string]bool{}
	for _, s := range selections {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" {
			names[s] = true
		}
	}
	if len(names) == 0 {
		return Plan{}, fmt.Errorf("at least one selection is required")
	}
	ordered := make([]string, 0, len(names))
	for s := range names {
		ordered = append(ordered, s)
	}
	sort.Strings(ordered)
	var plan Plan
	var combined []string
	var legacy []string
	genericParams := map[string]any{}
	for k, v := range params {
		genericParams[k] = v
	}
	for _, name := range ordered {
		var cap *SelectionCapability
		for i := range selectionCatalog {
			c := &selectionCatalog[i]
			if c.Resource == resource && c.Name == name {
				cap = c
				break
			}
		}
		if cap == nil {
			if supportsLegacy(resource, name) {
				legacy = append(legacy, name)
				continue
			}
			return Plan{}, fmt.Errorf("unknown %s selection %q", resource, name)
		}
		if cap.CSV {
			return Plan{}, fmt.Errorf("%s/%s is CSV; use the exact text endpoint", resource, name)
		}
		if cap.Legacy {
			legacy = append(legacy, name)
			continue
		}
		if cap.Generic && !cap.Standalone {
			hasOptionalPath := false
			for _, ep := range cap.Endpoints {
				if len(ep.Required) == 0 {
					hasOptionalPath = true
				}
			}
			if !hasOptionalPath && len(cap.Endpoints) > 0 {
				for _, p := range cap.Endpoints[0].Required {
					v, ok := parameterValue(params, p)
					if !ok {
						return Plan{}, fmt.Errorf("selection %s/%s requires %s", resource, name, p)
					}
					if p != "id" {
						for _, other := range ordered {
							if other != name && other != "timestamp" && other != "lookup" {
								return Plan{}, fmt.Errorf("selection %s/%s uses %s as its target; batch separately from other ID domains", resource, name, p)
							}
						}
					}
					genericParams["id"] = v
				}
			}
			combined = append(combined, name)
			continue
		}
		var selected *SelectionEndpoint
		for i := range cap.Endpoints {
			ep := &cap.Endpoints[i]
			valid := true
			for _, p := range ep.Required {
				if _, ok := parameterValue(params, p); !ok {
					valid = false
				}
			}
			if valid && (selected == nil || len(ep.Required) > len(selected.Required)) {
				selected = ep
			}
		}
		if selected == nil {
			return Plan{}, fmt.Errorf("selection %s/%s requires path parameters", resource, name)
		}
		call, err := endpointCall(*selected, []string{name}, params)
		if err != nil {
			return Plan{}, err
		}
		if supportsLegacy(resource, name) {
			call.FallbackCalls = []PlannedCall{legacyCall(resource, []string{name}, params)}
		}
		plan.Calls = append(plan.Calls, call)
	}
	if len(combined) > 0 {
		p := allowedParams(genericParams, generic.Parameters)
		p["selections"] = strings.Join(combined, ",")
		call := PlannedCall{Operation: Operation{ID: generic.ID, Path: generic.Path, Method: "GET"}, Selections: combined, Params: p, Version: "v2"}
		for _, s := range combined {
			if supportsLegacy(resource, s) {
				call.FallbackCalls = append(call.FallbackCalls, legacyCall(resource, []string{s}, genericParams))
			}
		}
		plan.Calls = append([]PlannedCall{call}, plan.Calls...)
	}
	if len(legacy) > 0 {
		plan.Calls = append(plan.Calls, legacyCall(resource, legacy, params))
	}
	return plan, nil
}

func parameterValue(params map[string]any, name string) (any, bool) {
	if v, ok := params[name]; ok && v != nil {
		if wire, e := queryScalar(v); e == nil && wire != "" {
			return v, true
		}
	}
	if name != "id" {
		if v, ok := params["id"]; ok && v != nil {
			if wire, e := queryScalar(v); e == nil && wire != "" {
				return v, true
			}
		}
	}
	return nil, false
}
func allowedParams(params map[string]any, names []string) map[string]any {
	out := map[string]any{}
	for _, name := range names {
		if name == "key" || name == "selections" || name == "legacy" {
			continue
		}
		if v, ok := params[name]; ok {
			out[name] = v
		}
	}
	return out
}
func endpointCall(ep SelectionEndpoint, selections []string, params map[string]any) (PlannedCall, error) {
	path := ep.Path
	p := allowedParams(params, ep.Parameters)
	for _, name := range ep.Required {
		v, ok := parameterValue(params, name)
		if !ok {
			return PlannedCall{}, fmt.Errorf("missing path parameter %s", name)
		}
		wire, err := queryScalar(v)
		if err != nil {
			return PlannedCall{}, fmt.Errorf("path parameter %s: %w", name, err)
		}
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(wire))
		delete(p, name)
	}
	return PlannedCall{Operation: Operation{ID: ep.ID, Path: path, Method: "GET"}, Selections: selections, Params: p, Version: "v2"}, nil
}
func legacyCall(resource string, names []string, params map[string]any) PlannedCall {
	allow := append([]string{"id", "comment"}, legacyAllowedFilters...)
	p := allowedParams(params, allow)
	id := ""
	if v, ok := parameterValue(params, "id"); ok {
		wire, _ := queryScalar(v)
		id = url.PathEscape(wire)
	}
	delete(p, "id")
	p["selections"] = strings.Join(names, ",")
	return PlannedCall{Operation: Operation{ID: "legacy" + resource, Path: "/" + resource + "/" + id, Method: "GET"}, Selections: names, Params: p, Version: "v1", Fallback: true}
}

func (c *Client) RawSelections(ctx context.Context, resource string, selections []string, params map[string]any, opts RequestOptions) (ExecutionResult, error) {
	p, e := PlanSelections(resource, selections, params)
	if e != nil {
		return ExecutionResult{}, e
	}
	return c.Execute(ctx, p, opts)
}

// Selection binds a resource-specific token to its concrete response type.
// The unexported marker prevents cross-resource combinations in FetchPair.
type Selection[R, T any] struct{ name, resource string }

func (s Selection[R, T]) Name() string     { return s.name }
func (s Selection[R, T]) Resource() string { return s.resource }

// DecodeSelections is the custom-decoder escape hatch for documented legacy
// shapes and application-specific projections. The decoder sees the complete
// recursively merged response and successful-call execution report is retained.
func DecodeSelections[T any](ctx context.Context, c *Client, resource string, selections []string, params map[string]any, opts RequestOptions, decode func(json.RawMessage) (T, error)) (T, ExecutionReport, error) {
	var out T
	if decode == nil {
		return out, ExecutionReport{}, fmt.Errorf("decoder is required")
	}
	r, e := c.RawSelections(ctx, resource, selections, params, opts)
	if e != nil {
		return out, r.Report, e
	}
	out, e = decode(r.Data)
	return out, r.Report, e
}

type Pair[A, B any] struct {
	First  A
	Second B
	Report ExecutionReport
}

func FetchPair[R, A, B any](ctx context.Context, c *Client, a Selection[R, A], b Selection[R, B], params map[string]any, opts RequestOptions) (Pair[A, B], error) {
	var out Pair[A, B]
	r, e := c.RawSelections(ctx, a.resource, []string{a.name, b.name}, params, opts)
	out.Report = r.Report
	if e != nil {
		return out, e
	}
	if e = json.Unmarshal(r.Data, &out.First); e != nil {
		return out, e
	}
	e = json.Unmarshal(r.Data, &out.Second)
	return out, e
}
func Fetch[R, T any](ctx context.Context, c *Client, s Selection[R, T], params map[string]any, opts RequestOptions) (T, ExecutionReport, error) {
	var out T
	r, e := c.RawSelections(ctx, s.resource, []string{s.name}, params, opts)
	if e == nil {
		e = json.Unmarshal(r.Data, &out)
	}
	return out, r.Report, e
}
