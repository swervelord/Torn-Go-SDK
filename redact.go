package torn

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

// paramsString renders a generated Params struct for fmt without ever
// printing the deprecated Key field (P2.2): set fields are listed, the key is
// shown only as "***" plus at most four characters.
func paramsString(name string, v any) string {
	rv := reflect.ValueOf(v)
	var parts []string
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Type().Field(i)
		if !f.IsExported() {
			continue
		}
		fv := rv.Field(i)
		if fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				continue
			}
			fv = fv.Elem()
		}
		if f.Name == "Key" {
			parts = append(parts, "Key:"+redactKey(fmt.Sprint(fv.Interface())))
			continue
		}
		parts = append(parts, f.Name+":"+fmt.Sprintf("%v", fv.Interface()))
	}
	return name + "{" + strings.Join(parts, " ") + "}"
}

// redactedQuery copies a query map with any "key" entry redacted.
func redactedQuery(q map[string]any) map[string]any {
	if q == nil {
		return nil
	}
	out := make(map[string]any, len(q))
	for k, v := range q {
		if strings.EqualFold(k, "key") {
			v = redactKey(fmt.Sprint(v))
		}
		out[k] = v
	}
	return out
}

// String describes the options with any Query "key" redacted (P2.2).
func (o RequestOptions) String() string {
	keys := make([]string, 0, len(o.Query))
	q := redactedQuery(o.Query)
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s:%v", k, q[k])
	}
	return fmt.Sprintf("RequestOptions{Attempts:%d Timeout:%s Route:%q DisableFallback:%t AllowLegacyFallback:%t Query:map[%s] NoCache:%t Strict:%t}",
		o.Attempts, o.Timeout, o.Route, o.DisableFallback, o.AllowLegacyFallback, strings.Join(parts, " "), o.NoCache, o.Strict)
}

// GoString redacts like String.
func (o RequestOptions) GoString() string { return o.String() }

// Format redacts for every fmt verb.
func (o RequestOptions) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(o.String())) }

// MarshalJSON encodes the options with any Query "key" redacted.
func (o RequestOptions) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Attempts            int            `json:"attempts,omitempty"`
		Timeout             time.Duration  `json:"timeout,omitempty"`
		Route               string         `json:"route,omitempty"`
		DisableFallback     bool           `json:"disable_fallback,omitempty"`
		AllowLegacyFallback bool           `json:"allow_legacy_fallback,omitempty"`
		Query               map[string]any `json:"query,omitempty"`
		NoCache             bool           `json:"no_cache,omitempty"`
		Strict              bool           `json:"strict,omitempty"`
	}{o.Attempts, o.Timeout, o.Route, o.DisableFallback, o.AllowLegacyFallback, redactedQuery(o.Query), o.NoCache, o.Strict})
}
