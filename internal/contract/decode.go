package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// DecodeError describes why a JSON value does not fit a generated model
// (PARITY_SPEC P1.4). Path is a JSON path such as $.items[3].type.
type DecodeError struct {
	// Path locates the offending value, starting at "$".
	Path string
	// Expected names the expected JSON type, schema rule or property.
	Expected string
	// Value is a truncated excerpt of the offending JSON value.
	Value string
	// Err is the underlying cause, when one exists.
	Err error
}

func (e *DecodeError) Error() string {
	msg := e.Path + ": expected " + e.Expected
	if e.Value != "" {
		msg += ", got " + e.Value
	}
	return msg
}

// Unwrap returns the underlying cause.
func (e *DecodeError) Unwrap() error { return e.Err }

// at prefixes a path segment ("[3]" or ".name") while unwinding.
func (e *DecodeError) at(segment string) *DecodeError {
	if e == nil {
		return nil
	}
	e.Path = "$" + segment + strings.TrimPrefix(e.Path, "$")
	return e
}

func memberSegment(name string) string {
	for _, r := range name {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return "[" + strconv.Quote(name) + "]"
		}
	}
	if name == "" {
		return `[""]`
	}
	return "." + name
}

// At adds an object member to the path of a decode error from a nested value.
func At(err error, member string) error { return atSegment(err, memberSegment(member)) }

func atSegment(err error, segment string) error {
	var de *DecodeError
	if errors.As(err, &de) {
		de.at(segment)
		return err
	}
	return (&DecodeError{Path: "$", Expected: "decodable value", Err: err}).at(segment)
}

// Missing reports an absent required property.
func Missing(member string) error {
	return &DecodeError{Path: "$", Expected: "required property " + member, Value: "missing"}
}

// NotVariant reports a union accessor used for a variant the value does not match.
func NotVariant(union string, variant int, raw []byte) error {
	return &DecodeError{Path: "$", Expected: fmt.Sprintf("%s variant %d", union, variant), Value: excerpt(raw)}
}

const excerptLimit = 64

func excerpt(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > excerptLimit {
		cut := excerptLimit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	return s
}

func valueExcerpt(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return excerpt(b)
}

// Excerpt truncates a raw JSON value for error messages.
func Excerpt(raw []byte) string { return excerpt(raw) }

var nullLiteral = []byte("null")

func isNull(data []byte) bool { return bytes.Equal(bytes.TrimSpace(data), nullLiteral) }

// DecodeString decodes a JSON string field without reflection.
func DecodeString(data []byte, out *string) error {
	if len(data) >= 2 && data[0] == '"' && data[len(data)-1] == '"' && bytes.IndexByte(data[1:len(data)-1], '\\') < 0 && bytes.IndexByte(data[1:len(data)-1], '"') < 0 && utf8.Valid(data) {
		ok := true
		for _, c := range data[1 : len(data)-1] {
			if c < 0x20 {
				ok = false
				break
			}
		}
		if ok {
			*out = string(data[1 : len(data)-1])
			return nil
		}
	}
	if len(data) == 0 || data[0] != '"' {
		return &DecodeError{Path: "$", Expected: "string", Value: excerpt(data)}
	}
	return DecodeInto(data, out)
}

// DecodeInt decodes a JSON integer exactly, accepting 1.0 and 1e3 spellings.
func DecodeInt(data []byte, out *int64) error {
	if n, err := strconv.ParseInt(string(data), 10, 64); err == nil {
		*out = n
		return nil
	}
	if len(data) == 0 || !(data[0] == '-' || data[0] >= '0' && data[0] <= '9') {
		return &DecodeError{Path: "$", Expected: "integer", Value: excerpt(data)}
	}
	n, err := integerValue(json.Number(bytes.TrimSpace(data)))
	if err != nil {
		return &DecodeError{Path: "$", Expected: "integer", Value: excerpt(data), Err: err}
	}
	*out = n
	return nil
}

// DecodeBool decodes a JSON boolean.
func DecodeBool(data []byte, out *bool) error {
	switch string(data) {
	case "true":
		*out = true
	case "false":
		*out = false
	default:
		return &DecodeError{Path: "$", Expected: "boolean", Value: excerpt(data)}
	}
	return nil
}

// DecodeNumber decodes a JSON number exactly as written.
func DecodeNumber(data []byte, out *json.Number) error {
	s := string(bytes.TrimSpace(data))
	if len(s) == 0 || !(s[0] == '-' || s[0] >= '0' && s[0] <= '9') || !json.Valid(data) {
		return &DecodeError{Path: "$", Expected: "number", Value: excerpt(data)}
	}
	*out = json.Number(s)
	return nil
}

var (
	unmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	rawMessageType  = reflect.TypeOf(json.RawMessage(nil))
)

// DecodeInto unmarshals data into out with exact integers: canonical values
// take the encoding/json fast path; integral spellings such as 1.0 or 1e3 are
// canonicalized and retried. Failures become *DecodeError with a JSON path.
func DecodeInto(data []byte, out any) error {
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("decode target must be a non-nil pointer")
	}
	t := rv.Type().Elem()
	if isNull(data) && requiresValue(t) {
		return &DecodeError{Path: "$", Expected: jsonKind(t), Value: "null"}
	}
	err := json.Unmarshal(data, out)
	if err == nil {
		return nil
	}
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) && strings.HasPrefix(te.Value, "number") {
		if canonical, ok := canonicalIntegers(data); ok {
			if json.Unmarshal(canonical, out) == nil {
				return nil
			}
		}
	}
	return locate(data, t, err)
}

func requiresValue(t reflect.Type) bool {
	if reflect.PointerTo(t).Implements(unmarshalerType) || t == rawMessageType {
		return false
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Interface:
		return false
	}
	return true
}

func jsonKind(t reflect.Type) string {
	if t == reflect.TypeOf(json.Number("")) {
		return "number"
	}
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	}
	return t.String()
}

// locate turns an encoding/json failure into a DecodeError, descending into
// arrays and maps to find the failing element (error path only).
func locate(data []byte, t reflect.Type, cause error) error {
	var de *DecodeError
	if errors.As(cause, &de) && !(t.Kind() == reflect.Slice && t != rawMessageType || t.Kind() == reflect.Map) {
		return cause
	}
	switch {
	case t.Kind() == reflect.Slice && t != rawMessageType:
		var items []json.RawMessage
		if json.Unmarshal(data, &items) == nil {
			for i, item := range items {
				if err := DecodeInto(item, reflect.New(t.Elem()).Interface()); err != nil {
					return atSegment(err, fmt.Sprintf("[%d]", i))
				}
			}
		}
	case t.Kind() == reflect.Map:
		var members map[string]json.RawMessage
		if json.Unmarshal(data, &members) == nil {
			for _, k := range sortedKeys(members) {
				if err := DecodeInto(members[k], reflect.New(t.Elem()).Interface()); err != nil {
					return At(err, k)
				}
			}
		}
	}
	if errors.As(cause, &de) {
		return cause
	}
	var syntax *json.SyntaxError
	if errors.As(cause, &syntax) {
		return &DecodeError{Path: "$", Expected: "valid JSON", Value: excerpt(data), Err: cause}
	}
	return &DecodeError{Path: "$", Expected: jsonKind(t), Value: excerpt(data), Err: cause}
}

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// canonicalIntegers rewrites integral JSON numbers (1.0, 1e3) in their
// canonical form. Only used for values bound to integer-only Go containers.
func canonicalIntegers(data []byte) ([]byte, bool) {
	v, err := Parse(data)
	if err != nil {
		return nil, false
	}
	changed := false
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case json.Number:
			if n, e := integerValue(t); e == nil {
				c := json.Number(strconv.FormatInt(n, 10))
				if c != t {
					changed = true
				}
				return c
			}
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
		case map[string]any:
			for k := range t {
				t[k] = walk(t[k])
			}
		}
		return x
	}
	v = walk(v)
	if !changed {
		return nil, false
	}
	b, err := json.Marshal(v)
	return b, err == nil
}

// SplitObject splits one JSON object into its members. It rejects duplicate
// member names and malformed structure; member values are returned verbatim.
func SplitObject(data []byte) (map[string]json.RawMessage, error) {
	i := skipSpace(data, 0)
	if i >= len(data) || data[i] != '{' {
		return nil, &DecodeError{Path: "$", Expected: "object", Value: excerpt(data)}
	}
	fields := make(map[string]json.RawMessage)
	i = skipSpace(data, i+1)
	if i < len(data) && data[i] == '}' {
		if skipSpace(data, i+1) != len(data) {
			return nil, malformed(data)
		}
		return fields, nil
	}
	for {
		if i >= len(data) || data[i] != '"' {
			return nil, malformed(data)
		}
		end, ok := scanString(data, i)
		if !ok {
			return nil, malformed(data)
		}
		keyRaw := data[i:end]
		var key string
		if bytes.IndexByte(keyRaw, '\\') >= 0 {
			if json.Unmarshal(keyRaw, &key) != nil {
				return nil, malformed(data)
			}
		} else {
			key = string(keyRaw[1 : len(keyRaw)-1])
		}
		i = skipSpace(data, end)
		if i >= len(data) || data[i] != ':' {
			return nil, malformed(data)
		}
		i = skipSpace(data, i+1)
		vend, ok := scanValue(data, i)
		if !ok {
			return nil, malformed(data)
		}
		if _, dup := fields[key]; dup {
			return nil, &DecodeError{Path: "$", Expected: "unique property names", Value: strconv.Quote(key), Err: fmt.Errorf("duplicate JSON property %q", key)}
		}
		fields[key] = json.RawMessage(data[i:vend])
		i = skipSpace(data, vend)
		if i >= len(data) {
			return nil, malformed(data)
		}
		if data[i] == '}' {
			if skipSpace(data, i+1) != len(data) {
				return nil, malformed(data)
			}
			return fields, nil
		}
		if data[i] != ',' {
			return nil, malformed(data)
		}
		i = skipSpace(data, i+1)
	}
}

func malformed(data []byte) error {
	return &DecodeError{Path: "$", Expected: "valid JSON object", Value: excerpt(data)}
}

func skipSpace(data []byte, i int) int {
	for i < len(data) && (data[i] == ' ' || data[i] == '\n' || data[i] == '\r' || data[i] == '\t') {
		i++
	}
	return i
}

// scanString returns the index after the closing quote of the string at i.
func scanString(data []byte, i int) (int, bool) {
	for j := i + 1; j < len(data); j++ {
		switch data[j] {
		case '\\':
			j++
		case '"':
			return j + 1, true
		}
	}
	return 0, false
}

// scanValue returns the index after the JSON value starting at i. Nested
// containers are matched structurally; their content is validated when the
// member is decoded.
func scanValue(data []byte, i int) (int, bool) {
	if i >= len(data) {
		return 0, false
	}
	switch data[i] {
	case '"':
		return scanString(data, i)
	case '{', '[':
		depth := 0
		for j := i; j < len(data); j++ {
			switch data[j] {
			case '"':
				end, ok := scanString(data, j)
				if !ok {
					return 0, false
				}
				j = end - 1
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return j + 1, true
				}
			}
		}
		return 0, false
	default:
		j := i
		for j < len(data) && !(data[j] == ',' || data[j] == '}' || data[j] == ']' || data[j] == ' ' || data[j] == '\n' || data[j] == '\r' || data[j] == '\t') {
			j++
		}
		if j == i || !json.Valid(data[i:j]) {
			return 0, false
		}
		return j, true
	}
}
