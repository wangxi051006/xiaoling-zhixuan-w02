package fulfillment

import (
	"bytes"
	"campus.local/xiaoling/internal/contract"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid fulfillment input")
var ErrInvalidState = errors.New("invalid fulfillment state")
var ErrNotFound = errors.New("fulfillment object not found")
var ErrQuoteChanged = errors.New("quote changed")
var ErrTaskClaimed = errors.New("task already claimed")
var ErrThrottled = errors.New("urge throttled")
var ErrIdempotency = errors.New("idempotency conflict")
var ErrNotReady = errors.New("fulfillment not ready")

func validText(s string, max int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) >= 1 && utf8.RuneCountInString(s) <= max && strings.TrimSpace(s) != "" && !strings.ContainsRune(s, 0)
}
func validID(s string) bool { return validText(s, 128) }
func normalizeLines(in []contract.OrderLineInput) ([]contract.OrderLineInput, error) {
	if len(in) == 0 || len(in) > 50 {
		return nil, ErrInvalid
	}
	counts := map[string]int32{}
	var total int32
	for _, l := range in {
		if !validID(l.SKUID) || l.Qty < 1 || l.Qty > 999 {
			return nil, ErrInvalid
		}
		total += l.Qty
		if total > 999 {
			return nil, ErrInvalid
		}
		counts[l.SKUID] += l.Qty
	}
	out := make([]contract.OrderLineInput, 0, len(counts))
	for id, n := range counts {
		out = append(out, contract.OrderLineInput{SKUID: id, Qty: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SKUID < out[j].SKUID })
	return out, nil
}
func selfCancelable(mode contract.DeliveryMode, status contract.OrderStatus, stock contract.StockStatus, dispatch, now time.Time) bool {
	return stock == contract.StockStatusUnsorted && ((mode == contract.DeliveryModeScheduled && status == contract.OrderStatusScheduled && now.Before(dispatch)) || (mode == contract.DeliveryModeAsap && status == contract.OrderStatusPreparing))
}

// Reject duplicate keys, null values, casing aliases and unknown fields before typed decoding.
func exactJSON(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, err := jsonValue(d)
	if err != nil {
		return ErrInvalid
	}
	if _, err = d.Token(); err != io.EOF {
		return ErrInvalid
	}
	if _, ok := v.(map[string]any); !ok {
		return ErrInvalid
	}
	if !jsonShape(v, reflect.TypeOf(out).Elem()) {
		return ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err = d.Decode(out); err != nil {
		return ErrInvalid
	}
	return nil
}
func jsonValue(d *json.Decoder) (any, error) {
	t, e := d.Token()
	if e != nil || t == nil {
		return nil, ErrInvalid
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, e
			}
			s, ok := k.(string)
			if !ok {
				return nil, ErrInvalid
			}
			if _, ok = m[s]; ok {
				return nil, ErrInvalid
			}
			v, e := jsonValue(d)
			if e != nil {
				return nil, e
			}
			m[s] = v
		}
		_, e = d.Token()
		return m, e
	case '[':
		a := []any{}
		for d.More() {
			v, e := jsonValue(d)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		_, e = d.Token()
		return a, e
	}
	return nil, ErrInvalid
}
func jsonShape(v any, t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if tag != "" && tag != "-" {
				fields[tag] = f.Type
			}
		}
		for k, x := range m {
			f, ok := fields[k]
			if !ok || !jsonShape(x, f) {
				return false
			}
		}
		return true
	case reflect.Slice:
		a, ok := v.([]any)
		if !ok {
			return false
		}
		for _, x := range a {
			if !jsonShape(x, t.Elem()) {
				return false
			}
		}
		return true
	case reflect.Bool:
		_, ok := v.(bool)
		return ok
	case reflect.String:
		_, ok := v.(string)
		return ok
	default:
		_, ok := v.(json.Number)
		return ok
	}
}
