//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
)

// unmarshalStoredJSON decodes JSON columns returned through toJSONString.
//
// Older ClickHouse JSON implementations can retain a JSON document as a JSON
// string. In that case toJSONString returns a quoted JSON document, while
// newer implementations return the document directly. Accept both forms so
// that readers remain compatible with existing tables.
func unmarshalStoredJSON(data string, value any) error {
	raw := []byte(data)
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		raw = []byte(encoded)
	}

	firstErr := json.Unmarshal(raw, value)
	if firstErr == nil {
		return nil
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return firstErr
	}
	normalized := normalizeStoredJSON(decoded, reflect.TypeOf(value), false)
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return firstErr
	}
	return json.Unmarshal(canonical, value)
}

func normalizeStoredJSON(value any, target reflect.Type, nested bool) any {
	if target == nil {
		return value
	}
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	if nested && reflect.PointerTo(target).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		return value
	}

	switch target.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return value
		}
		for name, child := range object {
			if field, ok := jsonStructField(target, name); ok {
				object[name] = normalizeStoredJSON(child, field.Type, true)
			}
		}
	case reflect.Slice, reflect.Array:
		items, ok := value.([]any)
		if !ok {
			return value
		}
		for i, child := range items {
			items[i] = normalizeStoredJSON(child, target.Elem(), true)
		}
	case reflect.Map:
		object, ok := value.(map[string]any)
		if !ok || target.Key().Kind() != reflect.String {
			return value
		}
		for name, child := range object {
			object[name] = normalizeStoredJSON(child, target.Elem(), true)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if text, ok := value.(string); ok {
			if _, err := strconv.ParseInt(text, 10, target.Bits()); err == nil {
				return json.Number(text)
			}
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if text, ok := value.(string); ok {
			if _, err := strconv.ParseUint(text, 10, target.Bits()); err == nil {
				return json.Number(text)
			}
		}
	case reflect.Float32, reflect.Float64:
		if text, ok := value.(string); ok {
			if _, err := strconv.ParseFloat(text, target.Bits()); err == nil {
				return json.Number(text)
			}
		}
	case reflect.Bool:
		if text, ok := value.(string); ok {
			if parsed, err := strconv.ParseBool(text); err == nil {
				return parsed
			}
		}
	}
	return value
}

func jsonStructField(target reflect.Type, name string) (reflect.StructField, bool) {
	for i := 0; i < target.NumField(); i++ {
		field := target.Field(i)
		if !field.IsExported() {
			continue
		}
		jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
		if jsonName == "-" {
			continue
		}
		if jsonName == "" {
			jsonName = field.Name
		}
		if jsonName == name {
			return field, true
		}
		if field.Anonymous {
			fieldType := field.Type
			for fieldType.Kind() == reflect.Pointer {
				fieldType = fieldType.Elem()
			}
			if fieldType.Kind() == reflect.Struct {
				if nested, ok := jsonStructField(fieldType, name); ok {
					return nested, true
				}
			}
		}
	}
	return reflect.StructField{}, false
}
