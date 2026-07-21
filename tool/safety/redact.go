//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

const redacted = "[REDACTED]"

const maxRedactionDepth = 64

type redactionVisit struct {
	kind reflect.Kind
	ptr  uintptr
}

var (
	secretKeyPattern    = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:api[_-]?key|(?:access|session)?[_-]?token|auth(?:orization)?|bearer|client[_-]?secret|credential|passwd|password|private[_-]?key|secret)(?:$|[^a-z0-9])`)
	secretValuePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:sk|rk|pk|ghp|gho|github_pat|xox[baprs])[-_][A-Za-z0-9_-]{12,}\b`),
		regexp.MustCompile(`(?i)\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
		regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/-]{10,}={0,2}`),
		regexp.MustCompile(`-----BEGIN (?:[A-Z0-9 ]+ )?PRIVATE KEY-----[\s\S]*?-----END (?:[A-Z0-9 ]+ )?PRIVATE KEY-----`),
		regexp.MustCompile(`(?i)((?:api[_-]?key|(?:access|session)?[_-]?token|client[_-]?secret|password|passwd|secret)\s*[:=]\s*")[^"\r\n]*"`),
		regexp.MustCompile(`(?i)((?:api[_-]?key|(?:access|session)?[_-]?token|client[_-]?secret|password|passwd|secret)\s*[:=]\s*')[^'\r\n]*'`),
		regexp.MustCompile(`(?i)((?:api[_-]?key|(?:access|session)?[_-]?token|client[_-]?secret|password|passwd|secret)\s*[:=]\s*["']?)[^\s,"';}]+`),
	}
)

// RedactString removes common secret material. It is deliberately always on
// and is not configurable by Policy.
func RedactString(value string) string {
	out := value
	for _, pattern := range secretValuePatterns {
		if pattern.NumSubexp() > 0 {
			replacement := `${1}` + redacted
			if strings.HasSuffix(pattern.String(), `*"`) {
				replacement += `"`
			} else if strings.HasSuffix(pattern.String(), `*'`) {
				replacement += `'`
			}
			out = pattern.ReplaceAllString(out, replacement)
		} else {
			out = pattern.ReplaceAllString(out, redacted)
		}
	}
	return out
}

func containsSecret(value string) bool {
	return RedactString(value) != value
}

// RedactValue recursively redacts maps, slices, strings, byte slices, and
// JSON-serializable structs. The bool reports whether the value changed.
func RedactValue(value any) (any, bool) {
	return redactValueDepth(value, "", 0, make(map[redactionVisit]struct{}))
}

func redactValue(value any, key string) (any, bool) {
	return redactValueDepth(value, key, 0, make(map[redactionVisit]struct{}))
}

func redactValueDepth(value any, key string, depth int, seen map[redactionVisit]struct{}) (any, bool) {
	if value == nil {
		return nil, false
	}
	if secretKeyPattern.MatchString(key) {
		return redacted, true
	}
	if depth >= maxRedactionDepth {
		return redacted, true
	}
	rv := reflect.ValueOf(value)
	if visit, ok := redactionIdentity(rv); ok {
		if _, exists := seen[visit]; exists {
			return redacted, true
		}
		seen[visit] = struct{}{}
		defer delete(seen, visit)
	}
	if out, changed, handled := redactConcreteValue(value, key, depth, seen); handled {
		return out, changed
	}
	if out, changed, handled := redactNamedScalar(rv); handled {
		return out, changed
	}
	if customSerializationContainsSecret(value, depth, seen) {
		return redacted, true
	}
	return redactReflectValue(rv, value, key, depth, seen)
}

func redactConcreteValue(
	value any,
	key string,
	depth int,
	seen map[redactionVisit]struct{},
) (any, bool, bool) {
	switch typed := value.(type) {
	case string:
		out := RedactString(typed)
		return out, out != typed, true
	case []byte:
		out := RedactString(string(typed))
		return []byte(out), out != string(typed), true
	case map[string]any:
		out := make(map[string]any, len(typed))
		changed := false
		for k, v := range typed {
			redactedKey := RedactString(k)
			redactedValue, didChange := redactValueDepth(v, k, depth+1, seen)
			out[redactedKey] = redactedValue
			changed = changed || redactedKey != k || didChange
		}
		return out, changed, true
	case map[string]string:
		out := make(map[string]string, len(typed))
		changed := false
		for k, v := range typed {
			redactedKey := RedactString(k)
			if secretKeyPattern.MatchString(k) {
				out[redactedKey] = redacted
				changed = true
				continue
			}
			out[redactedKey] = RedactString(v)
			changed = changed || redactedKey != k || out[redactedKey] != v
		}
		return out, changed, true
	case []any:
		out := make([]any, len(typed))
		changed := false
		for i, item := range typed {
			redactedValue, didChange := redactValueDepth(item, key, depth+1, seen)
			out[i] = redactedValue
			changed = changed || didChange
		}
		return out, changed, true
	}
	return nil, false, false
}

func redactNamedScalar(rv reflect.Value) (any, bool, bool) {
	if rv.Kind() == reflect.String {
		original := rv.String()
		redactedString := RedactString(original)
		out := reflect.New(rv.Type()).Elem()
		out.SetString(redactedString)
		return out.Interface(), redactedString != original, true
	}
	if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
		original := make([]byte, rv.Len())
		for i := range original {
			original[i] = byte(rv.Index(i).Uint())
		}
		redactedBytes := []byte(RedactString(string(original)))
		out := reflect.MakeSlice(rv.Type(), len(redactedBytes), len(redactedBytes))
		for i, b := range redactedBytes {
			out.Index(i).SetUint(uint64(b))
		}
		return out.Interface(), !reflect.DeepEqual(redactedBytes, original), true
	}
	return nil, false, false
}

func redactReflectValue(
	rv reflect.Value,
	value any,
	key string,
	depth int,
	seen map[redactionVisit]struct{},
) (any, bool) {
	if rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return value, false
		}
		return redactValueDepth(rv.Elem().Interface(), key, depth+1, seen)
	}
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return value, false
		}
		safe, changed := redactValueDepth(rv.Elem().Interface(), key, depth+1, seen)
		if !changed {
			return value, false
		}
		out := reflect.New(rv.Type().Elem())
		if !setRedactedReflectValue(out.Elem(), safe) {
			return redacted, true
		}
		return out.Interface(), true
	}
	switch rv.Kind() {
	case reflect.Struct:
		return redactReflectStruct(rv, depth, seen)
	case reflect.Map:
		return redactReflectMap(rv, depth, seen)
	case reflect.Slice:
		return redactReflectSlice(rv, key, depth, seen)
	case reflect.Array:
		return redactReflectArray(rv, key, depth, seen)
	case reflect.Chan, reflect.Func, reflect.Complex64, reflect.Complex128, reflect.UnsafePointer:
		return redacted, true
	default:
		return value, false
	}
}

func redactReflectStruct(rv reflect.Value, depth int, seen map[redactionVisit]struct{}) (any, bool) {
	out := reflect.New(rv.Type()).Elem()
	out.Set(rv)
	changed := false
	for i := 0; i < rv.NumField(); i++ {
		source := rv.Field(i)
		target := out.Field(i)
		if !source.CanInterface() || !target.CanSet() {
			continue
		}
		field := rv.Type().Field(i)
		fieldName := field.Name
		if tag := strings.Split(field.Tag.Get("json"), ",")[0]; tag != "" && tag != "-" {
			fieldName = tag
		}
		safe, didChange := redactValueDepth(source.Interface(), fieldName, depth+1, seen)
		if !didChange {
			continue
		}
		if !setRedactedReflectValue(target, safe) {
			return redacted, true
		}
		changed = true
	}
	return out.Interface(), changed
}

func redactReflectMap(rv reflect.Value, depth int, seen map[redactionVisit]struct{}) (any, bool) {
	if rv.IsNil() {
		return rv.Interface(), false
	}
	out := reflect.MakeMapWithSize(rv.Type(), rv.Len())
	changed := false
	iter := rv.MapRange()
	for iter.Next() {
		mapKey := iter.Key()
		if !safeRedactionMapKey(mapKey, depth, seen) {
			return redacted, true
		}
		safeKey := mapKey
		keyName := ""
		if mapKey.Kind() == reflect.String {
			keyName = mapKey.String()
			redactedKey := RedactString(keyName)
			if redactedKey != keyName {
				safeKey = reflect.New(mapKey.Type()).Elem()
				safeKey.SetString(redactedKey)
				changed = true
			}
		}
		safe, didChange := redactValueDepth(iter.Value().Interface(), keyName, depth+1, seen)
		target := reflect.New(rv.Type().Elem()).Elem()
		if !setRedactedReflectValue(target, safe) {
			return redacted, true
		}
		out.SetMapIndex(safeKey, target)
		changed = changed || didChange
	}
	return out.Interface(), changed
}

func customSerializationContainsSecret(value any, depth int, seen map[redactionVisit]struct{}) bool {
	if marshaler, ok := value.(json.Marshaler); ok {
		data, err := marshalJSONSafely(marshaler)
		if err != nil {
			return true
		}
		var generic any
		if err := json.Unmarshal(data, &generic); err != nil {
			return true
		}
		if _, changed := redactValueDepth(generic, "", depth+1, seen); changed {
			return true
		}
	}
	if marshaler, ok := value.(encoding.TextMarshaler); ok {
		data, err := marshalTextSafely(marshaler)
		return err != nil || containsSecret(string(data))
	}
	return false
}

func marshalJSONSafely(marshaler json.Marshaler) (data []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			data = nil
			err = fmt.Errorf("custom JSON marshaler panic: %v", recovered)
		}
	}()
	return marshaler.MarshalJSON()
}

func marshalTextSafely(marshaler encoding.TextMarshaler) (data []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			data = nil
			err = fmt.Errorf("custom text marshaler panic: %v", recovered)
		}
	}()
	return marshaler.MarshalText()
}

func safeRedactionMapKey(key reflect.Value, depth int, seen map[redactionVisit]struct{}) bool {
	if !key.IsValid() || !key.CanInterface() {
		return false
	}
	if customSerializationContainsSecret(key.Interface(), depth, seen) {
		return false
	}
	if _, ok := key.Interface().(encoding.TextMarshaler); ok {
		return true
	}
	switch key.Kind() {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	default:
		return false
	}
}

func redactReflectSlice(rv reflect.Value, key string, depth int, seen map[redactionVisit]struct{}) (any, bool) {
	if rv.IsNil() {
		return rv.Interface(), false
	}
	out := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
	changed := false
	for i := 0; i < rv.Len(); i++ {
		safe, didChange := redactValueDepth(rv.Index(i).Interface(), key, depth+1, seen)
		if !setRedactedReflectValue(out.Index(i), safe) {
			return redacted, true
		}
		changed = changed || didChange
	}
	return out.Interface(), changed
}

func redactReflectArray(rv reflect.Value, key string, depth int, seen map[redactionVisit]struct{}) (any, bool) {
	out := reflect.New(rv.Type()).Elem()
	changed := false
	for i := 0; i < rv.Len(); i++ {
		safe, didChange := redactValueDepth(rv.Index(i).Interface(), key, depth+1, seen)
		if !setRedactedReflectValue(out.Index(i), safe) {
			return redacted, true
		}
		changed = changed || didChange
	}
	return out.Interface(), changed
}

func setRedactedReflectValue(target reflect.Value, value any) bool {
	if !target.CanSet() {
		return false
	}
	if value == nil {
		switch target.Kind() {
		case reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
			target.SetZero()
			return true
		default:
			return false
		}
	}
	source := reflect.ValueOf(value)
	if source.Type().AssignableTo(target.Type()) {
		target.Set(source)
		return true
	}
	if source.Type().ConvertibleTo(target.Type()) {
		target.Set(source.Convert(target.Type()))
		return true
	}
	text, ok := value.(string)
	if !ok {
		return false
	}
	if target.Kind() == reflect.Slice && target.Type().Elem().Kind() == reflect.Uint8 {
		bytes := []byte(text)
		out := reflect.MakeSlice(target.Type(), len(bytes), len(bytes))
		for i, b := range bytes {
			out.Index(i).SetUint(uint64(b))
		}
		target.Set(out)
		return true
	}
	return false
}

func redactionIdentity(value reflect.Value) (redactionVisit, bool) {
	switch value.Kind() {
	case reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return redactionVisit{}, false
		}
		return redactionVisit{kind: value.Kind(), ptr: value.Pointer()}, true
	default:
		return redactionVisit{}, false
	}
}

func redactReason(value string) string {
	value = RedactString(value)
	return strings.TrimSpace(value)
}
