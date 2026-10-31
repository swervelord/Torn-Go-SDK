package torn

import (
	"context"
	"encoding/json"
	"errors"
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
	// Resource is the selection resource; codes 22/23 re-plan the call on the
	// other API version for it (P4.3).
	Resource string
	// Access is the highest documented key access level the call needs (P6.1).
	Access AccessLevel
	source map[string]any
}

// sourceParams returns the caller's original parameters for re-planning.
func (p PlannedCall) sourceParams() map[string]any {
	if p.source != nil {
		return p.source
	}
	return p.Params
}

// Plan is an ordered list of independently executable calls.
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
	// V2Fallback marks v1-only selections the v2 selection enum lists as
	// served by Torn's own v1 fallback; they are requested on v2 first.
	V2Fallback bool `json:"v2_fallback,omitempty"`
}

// SelectionEndpoint is one exact v2 path serving a selection.
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

// LegacySelections returns the v1 selection catalog.
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

// SupportsV1Selection reports whether the v1 catalog lists resource/name.
func SupportsV1Selection(resource, name string) bool {
	return supportsLegacy(strings.ToLower(strings.TrimSpace(resource)), strings.ToLower(strings.TrimSpace(name)))
}

// V1Catalog returns v1 selection names grouped by resource.
func V1Catalog() map[string][]string {
	out := map[string][]string{}
	for _, c := range legacySelectionCatalog {
		out[c.Resource] = append(out[c.Resource], c.Name)
	}
	return out
}

// AllowedV1Filters lists the query filters forwarded to v1 calls.
func AllowedV1Filters() []string { return append([]string(nil), legacyAllowedFilters...) }

// PlanV1Selections is an alias of PlanLegacySelections.
func PlanV1Selections(resource string, selections []string, params map[string]any) (Plan, error) {
	return PlanLegacySelections(resource, selections, params)
}

// PlanLegacySelections plans catalogued selections as one v1 call.
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
// map, in the caller's order (duplicates removed). Parameter values are never merged across independently supplied requests.
func PlanSelections(resource string, selections []string, params map[string]any) (Plan, error) {
	resource = strings.ToLower(strings.TrimSpace(resource))
	_, ok := genericCatalog[resource]
	if !ok {
		return Plan{}, fmt.Errorf("unknown resource %q", resource)
	}
	// Selections keep the caller's order (first occurrence wins), so the
	// combined request and a P4.5 split follow it.
	names := map[string]bool{}
	var ordered []string
	for _, s := range selections {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" && !names[s] {
			names[s] = true
			ordered = append(ordered, s)
		}
	}
	if len(names) == 0 {
		return Plan{}, fmt.Errorf("at least one selection is required")
	}
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
			// P1.5: a selection this SDK does not know yet is sent as a v2
			// generic selection and returned as raw JSON.
			combined = append(combined, name)
			continue
		}
		if cap.CSV {
			return Plan{}, fmt.Errorf("%s/%s is CSV; use the exact text endpoint", resource, name)
		}
		if cap.Legacy && cap.V2Fallback {
			combined = append(combined, name)
			continue
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
		forwardUnknown(call.Params, params, resource)
		call.Resource, call.source = resource, params
		if m, ok := operationMetadata[selected.ID]; ok {
			call.Access = m.AccessLevel
		}
		if supportsLegacy(resource, name) {
			call.FallbackCalls = []PlannedCall{legacyCall(resource, []string{name}, params)}
		}
		plan.Calls = append(plan.Calls, call)
	}
	if len(combined) > 0 {
		call := genericCall(resource, combined, genericParams)
		call.source = params
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

// genericCall plans selections on the v2 generic /{resource} path.
func genericCall(resource string, selections []string, params map[string]any) PlannedCall {
	generic := genericCatalog[resource]
	p := allowedParams(params, generic.Parameters)
	forwardUnknown(p, params, resource)
	p["selections"] = strings.Join(selections, ",")
	call := PlannedCall{Operation: Operation{ID: generic.ID, Path: generic.Path, Method: "GET"}, Selections: selections, Params: p, Version: "v2", Resource: resource, source: params}
	for _, s := range selections {
		if level, ok := SelectionAccessLevel(resource, s); ok && accessRank[level] > accessRank[call.Access] {
			call.Access = level
		}
	}
	return call
}

// documentedParams lists every parameter any endpoint of a resource documents.
func documentedParams(resource string) map[string]bool {
	out := map[string]bool{"id": true, "key": true, "selections": true, "legacy": true}
	for _, n := range genericCatalog[resource].Parameters {
		out[n] = true
	}
	for _, c := range selectionCatalog {
		if c.Resource != resource {
			continue
		}
		for _, ep := range c.Endpoints {
			for _, n := range ep.Parameters {
				out[n] = true
			}
			for _, n := range ep.Required {
				out[n] = true
			}
		}
	}
	for _, n := range legacyAllowedFilters {
		out[n] = true
	}
	return out
}

// forwardUnknown copies parameters no endpoint of the resource documents, so
// new upstream filters are forwarded verbatim instead of dropped (P1.5).
func forwardUnknown(dst, params map[string]any, resource string) {
	if len(params) == 0 {
		return
	}
	documented := documentedParams(resource)
	for k, v := range params {
		if !documented[k] {
			dst[k] = v
		}
	}
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
	forwardUnknown(p, params, resource)
	id := ""
	if v, ok := parameterValue(params, "id"); ok {
		wire, _ := queryScalar(v)
		id = url.PathEscape(wire)
	}
	delete(p, "id")
	p["selections"] = strings.Join(names, ",")
	return PlannedCall{Operation: Operation{ID: "legacy" + resource, Path: "/" + resource + "/" + id, Method: "GET"}, Selections: names, Params: p, Version: "v1", Fallback: true, Resource: resource, source: params}
}

// RawSelections plans and executes selections of one resource and returns
// the merged raw JSON. Unknown selection names are sent as v2 generic
// selections (P1.5).
func (c *Client) RawSelections(ctx context.Context, resource string, selections []string, params map[string]any, opts RequestOptions) (ExecutionResult, error) {
	p, e := PlanSelections(resource, selections, params)
	if e != nil {
		return ExecutionResult{}, e
	}
	return c.Execute(ctx, p, opts)
}

// Selection binds a resource-specific token to its concrete response type.
// The unexported marker prevents cross-resource combinations in FetchPair.
type Selection[R, T any] struct{ name, resource, schema string }

// Name returns the selection name.
func (s Selection[R, T]) Name() string { return s.name }

// Resource returns the selection's resource.
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

// Pair holds two selections decoded from one request. FirstErr and
// SecondErr report per-selection decode failures (P1.4).
type Pair[A, B any] struct {
	First     A
	Second    B
	Report    ExecutionReport
	FirstErr  error
	SecondErr error
}

// FetchPair requests two selections of one resource in one plan. Each
// selection is decoded independently: a decode failure of one does not
// discard the other (P1.4); the returned error joins both failures.
func FetchPair[R, A, B any](ctx context.Context, c *Client, a Selection[R, A], b Selection[R, B], params map[string]any, opts RequestOptions) (Pair[A, B], error) {
	var out Pair[A, B]
	r, e := c.RawSelections(ctx, a.resource, []string{a.name, b.name}, params, opts)
	out.Report = r.Report
	if e != nil {
		return out, e
	}
	out.FirstErr = c.decodeResponse(a.resource+"/"+a.name, a.schema, r.Data, &out.First, opts)
	out.SecondErr = c.decodeResponse(b.resource+"/"+b.name, b.schema, r.Data, &out.Second, opts)
	return out, errors.Join(out.FirstErr, out.SecondErr)
}

// Fetch requests one typed selection and decodes it tolerantly (strictly
// with Config.StrictDecoding or RequestOptions.Strict).
func Fetch[R, T any](ctx context.Context, c *Client, s Selection[R, T], params map[string]any, opts RequestOptions) (T, ExecutionReport, error) {
	var out T
	r, e := c.RawSelections(ctx, s.resource, []string{s.name}, params, opts)
	if e == nil {
		e = c.decodeResponse(s.resource+"/"+s.name, s.schema, r.Data, &out, opts)
	}
	return out, r.Report, e
}
