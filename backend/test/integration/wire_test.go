//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var wireOnce sync.Once
var wireDocument map[string]any
var wireErr error

func wireLoad() {
	data, e := os.ReadFile("../../../contracts/openapi.yaml")
	if e != nil {
		wireErr = e
		return
	}
	wireErr = json.Unmarshal(data, &wireDocument)
}
func (f *fixture) assertWire(method, path string, status int, data []byte) {
	f.t.Helper()
	wireOnce.Do(wireLoad)
	if wireErr != nil {
		f.t.Error(wireErr)
		return
	}
	path = strings.Split(path, "?")[0]
	paths := wireDocument["paths"].(map[string]any)
	var operation map[string]any
	for pattern, raw := range paths {
		if samePath(pattern, path) {
			operation, _ = raw.(map[string]any)[strings.ToLower(method)].(map[string]any)
			break
		}
	}
	if operation == nil {
		f.t.Errorf("wire operation absent %s %s", method, path)
		return
	}
	response, ok := operation["responses"].(map[string]any)[fmt.Sprint(status)].(map[string]any)
	if !ok {
		f.t.Errorf("unfrozen HTTP status %d for %s", status, path)
		return
	}
	content, ok := response["content"].(map[string]any)
	if !ok {
		return
	}
	schema := content["application/json"].(map[string]any)["schema"].(map[string]any)
	var v any
	if e := json.Unmarshal(data, &v); e != nil {
		f.t.Errorf("wire JSON %s: %v", path, e)
		return
	}
	if e := wireCheck(v, schema, "$"); e != nil {
		f.t.Errorf("frozen response %s %s: %v; body=%s", method, path, e, data)
	}
	if status >= 400 {
		for _, private := range []string{"SQLSTATE", "postgres://", "private synthetic", "INSERT INTO", "SELECT ", "private-room"} {
			if strings.Contains(string(data), private) {
				f.t.Errorf("private diagnostic leaked in %s", path)
			}
		}
	}
}
func samePath(pattern, path string) bool {
	a, b := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != "{id}" && a[i] != b[i] {
			return false
		}
	}
	return true
}
func wireCheck(v any, s map[string]any, path string) error {
	if ref, ok := s["$ref"].(string); ok {
		name := ref[strings.LastIndex(ref, "/")+1:]
		return wireCheck(v, wireDocument["components"].(map[string]any)["schemas"].(map[string]any)[name].(map[string]any), path)
	}
	if options, ok := s["oneOf"].([]any); ok {
		count := 0
		for _, o := range options {
			if wireCheck(v, o.(map[string]any), path) == nil {
				count++
			}
		}
		if count != 1 {
			return fmt.Errorf("%s violates oneOf", path)
		}
		return nil
	}
	if c, ok := s["const"]; ok && fmt.Sprint(v) != fmt.Sprint(c) {
		return fmt.Errorf("%s differs from const", path)
	}
	if enums, ok := s["enum"].([]any); ok {
		found := false
		for _, x := range enums {
			found = found || fmt.Sprint(x) == fmt.Sprint(v)
		}
		if !found {
			return fmt.Errorf("%s invalid enum", path)
		}
	}
	fail := func() error { return fmt.Errorf("%s violates %v", path, s) }
	switch s["type"] {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return fail()
		}
		props := s["properties"].(map[string]any)
		for _, x := range s["required"].([]any) {
			if _, ok := m[x.(string)]; !ok {
				return fmt.Errorf("%s missing %s", path, x)
			}
		}
		for k, x := range m {
			p, ok := props[k]
			if !ok {
				return fmt.Errorf("%s extra field %s", path, k)
			}
			if e := wireCheck(x, p.(map[string]any), path+"."+k); e != nil {
				return e
			}
		}
	case "array":
		a, ok := v.([]any)
		if !ok {
			return fail()
		}
		if min, ok := s["minItems"].(float64); ok && len(a) < int(min) {
			return fail()
		}
		if max, ok := s["maxItems"].(float64); ok && len(a) > int(max) {
			return fail()
		}
		for i, x := range a {
			if e := wireCheck(x, s["items"].(map[string]any), fmt.Sprintf("%s[%d]", path, i)); e != nil {
				return e
			}
		}
	case "string":
		x, ok := v.(string)
		if !ok {
			return fail()
		}
		n := utf8.RuneCountInString(x)
		if min, ok := s["minLength"].(float64); ok && n < int(min) {
			return fail()
		}
		if max, ok := s["maxLength"].(float64); ok && n > int(max) {
			return fail()
		}
		if p, ok := s["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(x) {
			return fail()
		}
		if s["format"] == "date-time" {
			if _, e := time.Parse(time.RFC3339Nano, x); e != nil {
				return fail()
			}
		}
		if s["format"] == "date" {
			if _, e := time.Parse(time.DateOnly, x); e != nil {
				return fail()
			}
		}
	case "integer":
		x, ok := v.(float64)
		if !ok || math.Trunc(x) != x {
			return fail()
		}
		if min, ok := s["minimum"].(float64); ok && x < min {
			return fail()
		}
		if max, ok := s["maximum"].(float64); ok && x > max {
			return fail()
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fail()
		}
	case "null":
		if v != nil {
			return fail()
		}
	}
	return nil
}
