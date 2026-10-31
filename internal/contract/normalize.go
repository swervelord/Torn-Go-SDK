package contract

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// integerValue accepts JSON's decimal/exponent notation without floating point
// conversion or allocating space proportional to an untrusted exponent.
func integerValue(n json.Number) (int64, error) {
	s := string(n)
	negative := strings.HasPrefix(s, "-")
	if negative {
		s = s[1:]
	}
	exponentText := "0"
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exponentText = s[i+1:]
		s = s[:i]
	}
	fraction := 0
	if i := strings.IndexByte(s, '.'); i >= 0 {
		fraction = len(s) - i - 1
		s = s[:i] + s[i+1:]
	}
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return 0, nil
	}
	exponent, e := strconv.ParseInt(exponentText, 10, 64)
	if e != nil {
		return 0, fmt.Errorf("integer exponent outside supported domain")
	}
	if exponent < -int64(len(s))-int64(fraction) || exponent > 20+int64(fraction) {
		return 0, fmt.Errorf("integer outside signed 64-bit domain")
	}
	shift := exponent - int64(fraction)
	if shift < 0 {
		remove := -shift
		if remove > int64(len(s)) {
			return 0, fmt.Errorf("expected integer")
		}
		suffix := s[len(s)-int(remove):]
		if strings.Trim(suffix, "0") != "" {
			return 0, fmt.Errorf("expected integer")
		}
		s = s[:len(s)-int(remove)]
	} else {
		if int64(len(s))+shift > 19 {
			return 0, fmt.Errorf("integer outside signed 64-bit domain")
		}
		s += strings.Repeat("0", int(shift))
	}
	if s == "" {
		s = "0"
	}
	if negative {
		s = "-" + s
	}
	v, e := strconv.ParseInt(s, 10, 64)
	if e != nil {
		return 0, fmt.Errorf("integer outside signed 64-bit domain")
	}
	return v, nil
}
