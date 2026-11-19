package settlement

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// Every field in these frozen command/manifest DTOs is required and non-null.
// Validate member spelling and uniqueness before Go's case-insensitive decoder
// can silently merge aliases or repeated members, including nested entries.
func decodeExactJSON(data []byte, out any) error {
	if !utf8.Valid(data) {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	var value func(reflect.Type) error
	value = func(t reflect.Type) error {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		switch t.Kind() {
		case reflect.Struct:
			tok, err := d.Token()
			if err != nil || tok != json.Delim('{') {
				return ErrInvalid
			}
			fields := map[string]reflect.Type{}
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				fields[strings.Split(f.Tag.Get("json"), ",")[0]] = f.Type
			}
			seen := map[string]bool{}
			for d.More() {
				tok, err = d.Token()
				if err != nil {
					return ErrInvalid
				}
				name, ok := tok.(string)
				ft, exists := fields[name]
				if !ok || !exists || seen[name] {
					return ErrInvalid
				}
				seen[name] = true
				if err := value(ft); err != nil {
					return err
				}
			}
			if tok, err = d.Token(); err != nil || tok != json.Delim('}') || len(seen) != len(fields) {
				return ErrInvalid
			}
		case reflect.Slice:
			tok, err := d.Token()
			if err != nil || tok != json.Delim('[') {
				return ErrInvalid
			}
			for d.More() {
				if err := value(t.Elem()); err != nil {
					return err
				}
			}
			if tok, err = d.Token(); err != nil || tok != json.Delim(']') {
				return ErrInvalid
			}
		default:
			var raw json.RawMessage
			if err := d.Decode(&raw); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return ErrInvalid
			}
			if err := json.Unmarshal(raw, reflect.New(t).Interface()); err != nil {
				return ErrInvalid
			}
		}
		return nil
	}
	if err := value(reflect.TypeOf(out)); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	if err := json.Unmarshal(data, out); err != nil {
		return ErrInvalid
	}
	return nil
}
