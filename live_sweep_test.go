package torn

// P9.2 live sweep: every typed operation reachable with the key owner's ids
// (plus public catalog ids such as item 206) is called and decoded in the
// default tolerant mode. Decode failures fail the test with their JSON path;
// API errors (no faction, access level, private data) are reported as skips.
// Gated by TORN_API_KEY or the legacy TORN_LIVE_TEST_API_KEY. Calls are spaced
// 0.7 s apart and back off 61 s on code 5 because other tools share the key.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

func liveKey(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"TORN_API_KEY", "TORN_LIVE_TEST_API_KEY"} {
		if k := strings.TrimSpace(os.Getenv(name)); k != "" {
			return k
		}
	}
	t.Skip("TORN_API_KEY (or TORN_LIVE_TEST_API_KEY) is not set")
	return ""
}

// publicIDs are documented catalog ids that are valid for every key.
var publicIDs = map[string]string{
	"typeId": "1", "propertyTypeId": "13", "categoryIds": "2", "crimeId": "1",
	"ids": "1", "logCategoryId": "1", "shopId": "1", "stockId": "1", "trackId": "6",
}

func TestLiveSweep(t *testing.T) {
	key := liveKey(t)
	c, err := NewClient(Config{APIKeys: []string{key}, Attempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var spec struct {
		Paths map[string]map[string]struct {
			ID         string `json:"operationId"`
			Parameters []struct {
				Ref      string `json:"$ref"`
				Name, In string
				Required bool
				Schema   map[string]any
			} `json:"parameters"`
		} `json:"paths"`
		Components struct {
			Parameters map[string]struct {
				Name, In string
				Required bool
				Schema   map[string]any
			} `json:"parameters"`
			Schemas map[string]map[string]any `json:"schemas"`
		} `json:"components"`
	}
	raw, err := os.ReadFile("spec/openapi-" + APIVersion + ".json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	pace := func() { time.Sleep(700 * time.Millisecond) }
	call := func(op Operation, params map[string]any) (json.RawMessage, error) {
		for attempt := 0; ; attempt++ {
			body, _, err := c.DoOperation(ctx, op, params, RequestOptions{Attempts: 1})
			pace()
			if code, ok := Code(err); ok && code == CodeTooManyRequests && attempt == 0 {
				t.Logf("code 5 on %s; backing off 61 s", op.ID)
				time.Sleep(61 * time.Second)
				c.mu.Lock()
				c.pool.keys[0].status = KeyHealthy
				c.mu.Unlock()
				continue
			}
			return body, err
		}
	}
	infoRaw, err := call(Operation{ID: "getKeyInfo", Path: "/key/info"}, nil)
	if err != nil {
		t.Fatalf("key/info: %v", err)
	}
	var info struct {
		Info struct {
			User struct {
				ID        int64  `json:"id"`
				FactionID *int64 `json:"faction_id"`
				CompanyID *int64 `json:"company_id"`
			} `json:"user"`
		} `json:"info"`
	}
	if err := json.Unmarshal(infoRaw, &info); err != nil {
		t.Fatal(err)
	}
	owner := map[string]string{"user": fmt.Sprint(info.Info.User.ID), "market": "206"}
	if f := info.Info.User.FactionID; f != nil {
		owner["faction"] = fmt.Sprint(*f)
	}
	if co := info.Info.User.CompanyID; co != nil {
		owner["company"] = fmt.Sprint(*co)
	}
	var paths []string
	for p := range spec.Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	decoded, apiErrors, skipped := 0, 0, 0
	var failures []string
	for _, path := range paths {
		op := spec.Paths[path]["get"]
		if op.ID == "" {
			continue
		}
		meta := operationMetadata[op.ID]
		params := map[string]any{}
		resource := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)[0]
		reason := ""
		for _, p := range op.Parameters {
			name, in, required, schema := p.Name, p.In, p.Required, p.Schema
			if p.Ref != "" {
				cp := spec.Components.Parameters[strings.TrimPrefix(p.Ref, "#/components/parameters/")]
				name, in, required, schema = cp.Name, cp.In, cp.Required, cp.Schema
			}
			if !required || name == "key" {
				continue
			}
			value := ""
			if in == "path" && name == "id" {
				value = owner[resource]
			} else if v, ok := publicIDs[name]; ok {
				value = v
			} else if e := firstEnum(schema, spec.Components.Schemas); e != "" {
				value = e // a documented value of a required enum parameter
			}
			if value == "" {
				reason = "needs " + name
				break
			}
			params[name] = value
		}
		if reason != "" {
			skipped++
			t.Logf("skip %-36s %s", op.ID, reason)
			continue
		}
		body, err := call(Operation{ID: op.ID, Path: path, Method: "GET", CSV: strings.Contains(path, "snapshot")}, params)
		var api *APIError
		if errors.As(err, &api) {
			apiErrors++
			t.Logf("api  %-36s code %d", op.ID, api.Code)
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", op.ID, err))
			continue
		}
		if meta.ResponseSchema == "" {
			decoded++
			continue
		}
		if _, err := DecodeOperation(op.ID, body); err != nil {
			var d *DecodeError
			if errors.As(err, &d) {
				failures = append(failures, fmt.Sprintf("%s: %s expected %s, got %s", op.ID, d.Path, d.Expected, d.Value))
			} else {
				failures = append(failures, fmt.Sprintf("%s: %v", op.ID, err))
			}
			continue
		}
		decoded++
	}
	t.Logf("live sweep: %d decoded, %d API errors, %d skipped (ids not reachable), %d failures", decoded, apiErrors, skipped, len(failures))
	for _, f := range failures {
		t.Error(f)
	}
}

// firstEnum returns the first documented value of an enum parameter schema.
func firstEnum(s map[string]any, schemas map[string]map[string]any) string {
	for depth := 0; s != nil && depth < 8; depth++ {
		if ref, ok := s["$ref"].(string); ok {
			s = schemas[strings.TrimPrefix(ref, "#/components/schemas/")]
			continue
		}
		if e, ok := s["enum"].([]any); ok && len(e) > 0 {
			return fmt.Sprint(e[0])
		}
		for _, k := range []string{"oneOf", "anyOf"} {
			if b, ok := s[k].([]any); ok && len(b) > 0 {
				if v := firstEnum(b[0].(map[string]any), schemas); v != "" {
					return v
				}
			}
		}
		if items, ok := s["items"].(map[string]any); ok {
			s = items
			continue
		}
		return ""
	}
	return ""
}
