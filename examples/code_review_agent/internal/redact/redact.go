//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package redact provides the single boundary for sanitizing review data.
package redact

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxInputBytes  = 1 << 20
	maxOutputBytes = 64 << 10
	maxValueDepth  = 64
	maxValueItems  = 10000

	inputTooLargeMarker = "[REDACTED:input-too-large]"
	truncatedMarker     = "\n...[TRUNCATED]...\n"
)

var (
	privateKeyPattern = regexp.MustCompile(
		`(?is)-----BEGIN(?: [A-Z0-9]+)* PRIVATE KEY-----.*?` +
			`-----END(?: [A-Z0-9]+)* PRIVATE KEY-----`,
	)
	assignmentPattern = regexp.MustCompile(
		`(?im)\b(` +
			`(?:[A-Za-z0-9]+[_-])*` +
			`(?:password|passwd|pwd|token|secret|credentials?|` +
			`(?:api|access|openai|private)[_-]key)` +
			`(?:[_-][A-Za-z0-9]+)*` +
			`)([ \t]*(?:=|:)[ \t]*)` +
			`("(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|[^\s,;}\]]+)`,
	)
	dsnURLPattern = regexp.MustCompile(
		`(?i)\b(postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|rediss|amqp|amqps)` +
			`://([^:/@\s]+):([^@\s/]+)@`,
	)
	mysqlDSNPattern = regexp.MustCompile(
		`(?i)\b([^:\s/@]+):([^@\s]+)@tcp\(`,
	)
	credentialURLPattern = regexp.MustCompile(
		`(?i)\b(https?|ftps?)://([^:/@\s]+):([^@\s/]+)@`,
	)
	authorizationBearerPattern = regexp.MustCompile(
		`(?i)(\bauthorization[ \t]*:[ \t]*bearer[ \t]+)([^\s,;]+)`,
	)
	standaloneBearerPattern = regexp.MustCompile(
		`(?i)(\bbearer[ \t]+)([A-Za-z0-9._~+/=-]{16,})`,
	)
	openAIKeyPattern = regexp.MustCompile(
		`\bsk-(?:(?:proj|test)-)?[A-Za-z0-9_-]{20,}\b`,
	)
)

// String redacts common credential shapes and returns bounded output. Inputs
// beyond the processing limit are replaced entirely to avoid partial leaks.
func String(value string) string {
	if len(value) > maxInputBytes {
		return inputTooLargeMarker
	}

	redacted := privateKeyPattern.ReplaceAllString(value, "[REDACTED:private-key]")
	redacted = assignmentPattern.ReplaceAllStringFunc(redacted, redactAssignment)
	redacted = dsnURLPattern.ReplaceAllString(
		redacted,
		`${1}://${2}:[REDACTED:dsn]@`,
	)
	redacted = mysqlDSNPattern.ReplaceAllString(
		redacted,
		`${1}:[REDACTED:dsn]@tcp(`,
	)
	redacted = credentialURLPattern.ReplaceAllString(
		redacted,
		`${1}://${2}:[REDACTED:url-password]@`,
	)
	redacted = authorizationBearerPattern.ReplaceAllString(
		redacted,
		`${1}[REDACTED:bearer-token]`,
	)
	redacted = standaloneBearerPattern.ReplaceAllString(
		redacted,
		`${1}[REDACTED:bearer-token]`,
	)
	redacted = openAIKeyPattern.ReplaceAllString(
		redacted,
		"[REDACTED:openai-api-key]",
	)
	return truncate(redacted)
}

// Error returns an error with its message passed through String. It returns nil
// when err is nil and deliberately does not retain an unwrap path to raw data.
func Error(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(String(err.Error()))
}

// Value returns a redacted copy of a JSON-compatible recursive value. Map keys
// are retained because they describe structure; string values are sanitized.
func Value(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	if number, ok := value.(json.Number); ok {
		return number, nil
	}

	items := 0
	redacted, err := redactValue(reflect.ValueOf(value), 0, &items)
	if err != nil {
		return nil, fmt.Errorf("redact value: %w", err)
	}
	return redacted.Interface(), nil
}

func redactAssignment(match string) string {
	parts := assignmentPattern.FindStringSubmatch(match)
	if len(parts) != 4 {
		return match
	}
	kind, ok := sensitiveKind(parts[1])
	if !ok {
		return match
	}
	return parts[1] + parts[2] + replaceValue(parts[3], kind)
}

func sensitiveKind(name string) (string, bool) {
	normalized := strings.ToLower(strings.ReplaceAll(name, "-", "_"))
	parts := strings.Split(normalized, "_")
	for i, part := range parts {
		switch part {
		case "password", "passwd", "pwd":
			return "password", true
		case "token":
			return "token", true
		case "secret":
			return "secret", true
		case "credential", "credentials":
			return "credential", true
		case "key":
			if i > 0 {
				switch parts[i-1] {
				case "api", "access", "secret", "openai":
					return "api-key", true
				case "private":
					return "private-key", true
				}
			}
		}
	}
	return "", false
}

func replaceValue(value, kind string) string {
	marker := "[REDACTED:" + kind + "]"
	if len(value) >= 2 {
		if value[0] == value[len(value)-1] &&
			(value[0] == '\'' || value[0] == '"') {
			return value[:1] + marker + value[len(value)-1:]
		}
	}
	return marker
}

func truncate(value string) string {
	if len(value) <= maxOutputBytes {
		return value
	}
	available := maxOutputBytes - len(truncatedMarker)
	headBytes := available / 2
	tailBytes := available - headBytes
	head := validPrefix(value, headBytes)
	tail := validSuffix(value, len(value)-tailBytes)
	return head + truncatedMarker + tail
}

func validPrefix(value string, limit int) string {
	if limit >= len(value) {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}

func validSuffix(value string, start int) string {
	if start <= 0 {
		return value
	}
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return value[start:]
}

func redactValue(value reflect.Value, depth int, items *int) (reflect.Value, error) {
	if depth > maxValueDepth {
		return reflect.Value{}, errors.New("maximum depth exceeded")
	}
	*items++
	if *items > maxValueItems {
		return reflect.Value{}, errors.New("maximum item count exceeded")
	}

	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		item, err := redactValue(value.Elem(), depth+1, items)
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(value.Type()).Elem()
		out.Set(item)
		return out, nil
	case reflect.String:
		out := reflect.New(value.Type()).Elem()
		out.SetString(String(value.String()))
		return out, nil
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32,
		reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32,
		reflect.Uint64, reflect.Float32, reflect.Float64:
		return value, nil
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return reflect.Value{}, fmt.Errorf("unsupported type %s", value.Type())
		}
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			item, err := redactValue(iter.Value(), depth+1, items)
			if err != nil {
				return reflect.Value{}, err
			}
			out.SetMapIndex(iter.Key(), item)
		}
		return out, nil
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			item, err := redactValue(value.Index(i), depth+1, items)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(item)
		}
		return out, nil
	case reflect.Array:
		out := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			item, err := redactValue(value.Index(i), depth+1, items)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(item)
		}
		return out, nil
	case reflect.Invalid:
		return reflect.Value{}, nil
	default:
		return reflect.Value{}, fmt.Errorf("unsupported type %s", value.Type())
	}
}
