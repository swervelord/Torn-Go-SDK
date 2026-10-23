// Package compat contains the independently maintained compatibility facade models.
// Generated models use exact integer types, aliases, defaults and flattened extras.
package compat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
)

type Null struct{}

func decodeVariant(data []byte, target any, allowNull bool) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		if !allowNull {
			return errors.New("null is not this variant")
		}
	}
	if _, ok := target.(*json.Number); ok {
		b := bytes.TrimSpace(data)
		if len(b) == 0 || !(b[0] == '-' || b[0] >= '0' && b[0] <= '9') {
			return errors.New("expected JSON number")
		}
	}
	return decodeCompat(data, target)
}

func decodeSpecial(name string, data []byte, target any) error {
	obj := map[string]json.RawMessage{}
	objectErr := json.Unmarshal(data, &obj)
	text := func(key string) string { var s string; _ = json.Unmarshal(obj[key], &s); return s }
	strptr := func(key string) *string { var p *string; _ = json.Unmarshal(obj[key], &p); return p }
	has := func(key string) bool { _, ok := obj[key]; return ok }
	switch v := target.(type) {
	case *UserEmployment:
		switch text("type") {
		case "job":
			var x UserCityJob
			if e := json.Unmarshal(data, &x); e != nil {
				return e
			}
			v.UserCityJob = &x
		case "company":
			var x UserCompanyJob
			if e := json.Unmarshal(data, &x); e != nil {
				return e
			}
			v.UserCompanyJob = &x
		default:
			if objectErr != nil || obj == nil {
				obj = map[string]json.RawMessage{"raw": append(json.RawMessage(nil), data...)}
			}
			v.UnknownUserEmployment = &UnknownUserEmployment{JobType: strptr("type"), Extra: obj}
		}
		return nil
	case *UserCompetition:
		var err error
		switch text("name") {
		case "Halloween":
			var x UserHalloweenCompetition
			err = json.Unmarshal(data, &x)
			v.UserHalloweenCompetition = &x
		case "Easter Egg Hunt":
			var x UserEasterCompetition
			err = json.Unmarshal(data, &x)
			v.UserEasterCompetition = &x
		case "Rock, Paper, Scissors":
			var x UserRpsCompetition
			err = json.Unmarshal(data, &x)
			v.UserRpsCompetition = &x
		case "Elimination":
			var x UserEliminationCompetition
			err = json.Unmarshal(data, &x)
			v.UserEliminationCompetition = &x
		default:
			if objectErr != nil || obj == nil {
				obj = map[string]json.RawMessage{"raw": append(json.RawMessage(nil), data...)}
			}
			v.UnknownUserCompetition = &UnknownUserCompetition{Name: strptr("name"), Extra: obj}
		}
		return err
	case *UserOrganizedCrimeSelection:
		if has("code") && has("error") {
			var x UserOrganizedCrimeError
			if e := json.Unmarshal(data, &x); e != nil {
				return e
			}
			v.UserOrganizedCrimeError = &x
		} else {
			var x *UserOrganizedCrime
			if e := json.Unmarshal(data, &x); e != nil {
				return e
			}
			v.UserOrganizedCrime = &x
		}
		return nil
	case *FactionWarfareEntry:
		switch {
		case has("territory") && has("result"):
			var x FactionTerritoryWarfareSummary
			if e := json.Unmarshal(data, &x); e != nil {
				return e
			}
			v.FactionTerritoryWarfareSummary = &x
		case has("aggressor") && has("defender"):
			var x FactionRaidSummary
			if e := json.Unmarshal(data, &x); e != nil {
				return e
			}
			v.FactionRaidSummary = &x
		case has("target") && has("winner") && has("factions"):
			var x FactionRankedWarDetails
			if e := json.Unmarshal(data, &x); e != nil {
				return e
			}
			v.FactionRankedWarDetails = &x
		case has("chain") && has("faction"):
			var x FactionChainWarfareSummary
			if e := json.Unmarshal(data, &x); e != nil {
				return e
			}
			v.FactionChainWarfareSummary = &x
		default:
			if objectErr != nil || obj == nil {
				obj = map[string]json.RawMessage{}
			}
			v.FactionUnknownWarfareEntry = &FactionUnknownWarfareEntry{Extra: obj}
		}
		return nil
	case *TradeItem:
		if objectErr != nil || obj == nil {
			return errors.New("expected trade object")
		}
		extra := map[string]json.RawMessage{}
		for k, value := range obj {
			if k != "user_id" && k != "type" && k != "details" {
				extra[k] = value
			}
		}
		normalized := map[string]json.RawMessage{}
		for k, value := range obj {
			normalized[k] = value
		}
		normalized["extra"], _ = json.Marshal(extra)
		if !has("details") {
			normalized["details"] = json.RawMessage("null")
		}
		payload, _ := json.Marshal(normalized)
		switch text("type") {
		case "Money":
			var x TradeMoneyItem
			if json.Unmarshal(payload, &x) == nil {
				v.TradeMoneyItem = &x
				return nil
			}
		case "Item":
			var x TradeInventoryItem
			if json.Unmarshal(payload, &x) == nil {
				v.TradeInventoryItem = &x
				return nil
			}
		case "Faction":
			var x TradeFactionItem
			if json.Unmarshal(payload, &x) == nil {
				v.TradeFactionItem = &x
				return nil
			}
		case "Company":
			var x TradeCompanyItem
			if json.Unmarshal(payload, &x) == nil {
				v.TradeCompanyItem = &x
				return nil
			}
		case "Property":
			var x TradePropertyItem
			if json.Unmarshal(payload, &x) == nil {
				v.TradePropertyItem = &x
				return nil
			}
		case "NAP":
			var x TradeNapItem
			if json.Unmarshal(payload, &x) == nil {
				v.TradeNapItem = &x
				return nil
			}
		}
		var userID *uint64
		if value, ok := obj["user_id"]; ok {
			if e := decodeCompat(value, &userID); e != nil {
				return e
			}
		}
		var details *DynamicValue
		if e := json.Unmarshal(normalized["details"], &details); e != nil {
			return e
		}
		v.UnknownTradeItem = &UnknownTradeItem{UserID: userID, ItemType: strptr("type"), Details: details, Extra: extra}
		return nil
	}
	return fmt.Errorf("unknown special union %s", name)
}

func (*Null) UnmarshalJSON(b []byte) error {
	if !bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return errors.New("expected null")
	}
	return nil
}
func (Null) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

type fieldRule struct {
	Name, Key                  string
	Default, Optional, Flatten bool
	Aliases                    []string
	Custom                     string
}

func decodeStruct(data []byte, target any, rules []fieldRule) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	if obj == nil {
		return errors.New("expected object")
	}
	v := reflect.ValueOf(target).Elem()
	used := map[string]bool{}
	for _, f := range rules {
		field := v.FieldByName(f.Name)
		if f.Flatten {
			continue
		}
		names := append([]string{f.Key}, f.Aliases...)
		seenNames := map[string]bool{}
		var raw json.RawMessage
		found := false
		for _, key := range names {
			if seenNames[key] {
				continue
			}
			seenNames[key] = true
			if b, ok := obj[key]; ok {
				if found {
					return fmt.Errorf("duplicate field or alias %s", f.Key)
				}
				raw = b
				found = true
				used[key] = true
			}
		}
		if !found {
			if f.Default {
				defaults(field)
				continue
			}
			if f.Optional {
				continue
			}
			return fmt.Errorf("missing required field %s", f.Key)
		}
		if f.Custom != "" {
			if err := decodeCustom(f.Custom, raw, field); err != nil {
				return fmt.Errorf("%s: %w", f.Key, err)
			}
			continue
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && field.Kind() != reflect.Pointer && field.Type() != reflect.TypeOf(json.RawMessage{}) {
			return fmt.Errorf("%s: null is not permitted", f.Key)
		}
		if err := decodeCompat(raw, field.Addr().Interface()); err != nil {
			return fmt.Errorf("%s: %w", f.Key, err)
		}
	}
	for _, f := range rules {
		if !f.Flatten {
			continue
		}
		extra := map[string]json.RawMessage{}
		for k, raw := range obj {
			if !used[k] {
				extra[k] = raw
			}
		}
		raw, _ := json.Marshal(extra)
		if err := decodeCompat(raw, v.FieldByName(f.Name).Addr().Interface()); err != nil {
			return err
		}
	}
	return nil
}
func defaults(v reflect.Value) {
	switch v.Kind() {
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 0, 0))
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				defaults(v.Field(i))
			}
		}
	}
}
func encodeStruct(v any) ([]byte, error) {
	value := reflect.ValueOf(v)
	typ := value.Type()
	obj := map[string]json.RawMessage{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		if tag == "-" {
			if value.Field(i).Kind() == reflect.Map {
				it := value.Field(i).MapRange()
				for it.Next() {
					raw, e := json.Marshal(it.Value().Interface())
					if e != nil {
						return nil, e
					}
					obj[it.Key().String()] = raw
				}
			}
			continue
		}
		raw, e := json.Marshal(value.Field(i).Interface())
		if e != nil {
			return nil, e
		}
		obj[tag] = raw
	}
	return json.Marshal(obj)
}
func decodeCustom(name string, raw json.RawMessage, v reflect.Value) error {
	switch name {
	case "optional_stringish":
		var value any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if e := d.Decode(&value); e != nil {
			return e
		}
		var s string
		switch x := value.(type) {
		case string:
			s = x
		case json.Number:
			s = x.String()
		default:
			v.SetZero()
			return nil
		}
		v.Set(reflect.ValueOf(&s))
		return nil
	case "optional_position_name":
		var s string
		if json.Unmarshal(raw, &s) == nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			v.Set(reflect.ValueOf(&s))
			return nil
		}
		var obj struct {
			Name *string `json:"name"`
		}
		if json.Unmarshal(raw, &obj) == nil {
			v.Set(reflect.ValueOf(obj.Name))
		} else {
			v.SetZero()
		}
		return nil
	case "company_news":
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			v.Set(reflect.MakeMap(v.Type()))
			return nil
		}
		var list []CompanyNewsEntry
		if len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '[' {
			if e := json.Unmarshal(raw, &list); e != nil {
				return e
			}
			out := map[string]CompanyNewsEntry{}
			for i, entry := range list {
				key := strconv.Itoa(i)
				if entry.ID != nil {
					key = *entry.ID
				} else if entry.Timestamp != nil {
					key = strconv.FormatUint(*entry.Timestamp, 10)
				}
				out[key] = entry
			}
			v.Set(reflect.ValueOf(out))
			return nil
		}
		return json.Unmarshal(raw, v.Addr().Interface())
	default:
		return errors.New("unknown custom decoder")
	}
}
