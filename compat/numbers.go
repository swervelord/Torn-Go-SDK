package compat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// decodeCompat normalizes only typed integer leaves. Raw JSON and custom
// unmarshalers retain their own wire contracts and original number spellings.
func decodeCompat(data []byte, target any) error {
	v := reflect.ValueOf(target)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() {
		return errors.New("compatibility decode target must be a nonnil pointer")
	}
	return decodeCompatValue(data, v.Elem())
}
func decodeCompatValue(data []byte, v reflect.Value) error {
	if v.CanAddr() {
		if _, ok := v.Addr().Interface().(json.Unmarshaler); ok {
			return json.Unmarshal(data, v.Addr().Interface())
		}
	}
	switch v.Kind() {
	case reflect.Pointer:
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			v.SetZero()
			return nil
		}
		n := reflect.New(v.Type().Elem())
		if e := decodeCompatValue(data, n.Elem()); e != nil {
			return e
		}
		v.Set(n)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		s, e := integerText(data)
		if e != nil {
			return e
		}
		n, e := strconv.ParseInt(s, 10, v.Type().Bits())
		if e != nil {
			return errors.New("integer outside signed domain")
		}
		v.SetInt(n)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		s, e := integerText(data)
		if e != nil {
			return e
		}
		n, e := strconv.ParseUint(s, 10, v.Type().Bits())
		if e != nil {
			return errors.New("integer outside unsigned domain")
		}
		v.SetUint(n)
		return nil
	case reflect.Slice, reflect.Array:
		var items []json.RawMessage
		if e := json.Unmarshal(data, &items); e != nil {
			return e
		}
		if items == nil {
			return errors.New("expected array")
		}
		var out reflect.Value
		if v.Kind() == reflect.Array {
			if len(items) != v.Len() {
				return errors.New("array has wrong length")
			}
			out = reflect.New(v.Type()).Elem()
		} else {
			out = reflect.MakeSlice(v.Type(), len(items), len(items))
		}
		for i, raw := range items {
			if e := decodeCompatValue(raw, out.Index(i)); e != nil {
				return fmt.Errorf("index %d: %w", i, e)
			}
		}
		v.Set(out)
		return nil
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return json.Unmarshal(data, v.Addr().Interface())
		}
		var items map[string]json.RawMessage
		if e := json.Unmarshal(data, &items); e != nil {
			return e
		}
		if items == nil {
			return errors.New("expected object")
		}
		out := reflect.MakeMapWithSize(v.Type(), len(items))
		for key, raw := range items {
			item := reflect.New(v.Type().Elem()).Elem()
			if e := decodeCompatValue(raw, item); e != nil {
				return fmt.Errorf("key %s: %w", key, e)
			}
			out.SetMapIndex(reflect.ValueOf(key).Convert(v.Type().Key()), item)
		}
		v.Set(out)
		return nil
	default:
		return json.Unmarshal(data, v.Addr().Interface())
	}
}

// integerText accepts integral decimal/exponent spellings without float64 or
// allocating zeros in proportion to an untrusted exponent. At most 20 output
// digits are ever allocated; signed/unsigned and bit bounds are checked above.
func integerText(raw []byte) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || !json.Valid(raw) || !(raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9') {
		return "", errors.New("expected integer")
	}
	s := string(raw)
	negative := strings.HasPrefix(s, "-")
	if negative {
		s = s[1:]
	}
	expText := "0"
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		expText = s[i+1:]
		s = s[:i]
	}
	fraction := 0
	if i := strings.IndexByte(s, '.'); i >= 0 {
		fraction = len(s) - i - 1
		s = s[:i] + s[i+1:]
	}
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0", nil
	}
	exponent, e := strconv.ParseInt(expText, 10, 64)
	if e != nil {
		return "", errors.New("integer exponent outside supported domain")
	}
	if exponent > int64(fraction)+20 || exponent < int64(fraction)-int64(len(s)) {
		return "", errors.New("integer outside domain or fractional")
	}
	shift := exponent - int64(fraction)
	if shift < 0 {
		remove := int(-shift)
		if remove > len(s) || strings.Trim(s[len(s)-remove:], "0") != "" {
			return "", errors.New("expected integral value")
		}
		s = s[:len(s)-remove]
	} else {
		if int64(len(s))+shift > 20 {
			return "", errors.New("integer outside domain")
		}
		s += strings.Repeat("0", int(shift))
	}
	if len(s) > 20 {
		return "", errors.New("integer outside domain")
	}
	if negative {
		s = "-" + s
	}
	return s, nil
}
