//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"unicode"
)

const (
	// RedactedValue replaces secret material in reports, audit events, and logs.
	RedactedValue = "[REDACTED]"

	maxReflectiveInspectionDepth = 32
	maxReflectiveInspectionNodes = 10_000
	maxReflectiveInspectionBytes = 1 << 20
)

// Redactor removes secret material without mutating the supplied value.
// Implementations must be safe for concurrent use.
type Redactor interface {
	RedactString(string) (string, int)
	RedactBytes([]byte) ([]byte, int)
	RedactValue(any) (any, int)
}

type patternRedactor struct {
	patterns     []redactionPattern
	sensitiveKey *regexp.Regexp
}

type redactionPattern struct {
	expression  *regexp.Regexp
	replacement string
}

type redactorChain struct {
	redactors []Redactor
}

func chainRedactors(redactors ...Redactor) Redactor {
	filtered := make([]Redactor, 0, len(redactors))
	for _, redactor := range redactors {
		if !isNilRedactor(redactor) {
			filtered = append(filtered, redactor)
		}
	}
	switch len(filtered) {
	case 0:
		return NewRedactor()
	case 1:
		return filtered[0]
	default:
		return &redactorChain{redactors: filtered}
	}
}

func (c *redactorChain) RedactString(value string) (string, int) {
	count := 0
	for _, redactor := range c.redactors {
		if isNilRedactor(redactor) {
			continue
		}
		clean, found := redactStringSafely(redactor, value)
		value = clean
		count += found
	}
	return value, count
}

func (c *redactorChain) RedactBytes(value []byte) ([]byte, int) {
	count := 0
	for _, redactor := range c.redactors {
		if isNilRedactor(redactor) {
			continue
		}
		clean, found := redactBytesSafely(redactor, value)
		value = clean
		count += found
	}
	return value, count
}

func (c *redactorChain) RedactValue(value any) (any, int) {
	count := 0
	for _, redactor := range c.redactors {
		if isNilRedactor(redactor) {
			continue
		}
		clean, found := redactValueSafely(redactor, value)
		value = clean
		count += found
	}
	return value, count
}

func redactStringSafely(
	redactor Redactor,
	value string,
) (clean string, count int) {
	clean = value
	defer func() {
		if recover() != nil {
			clean = RedactedValue
			count = 1
		}
	}()
	return redactor.RedactString(value)
}

func redactBytesSafely(
	redactor Redactor,
	value []byte,
) (clean []byte, count int) {
	clean = append([]byte(nil), value...)
	defer func() {
		if recover() != nil {
			clean = []byte(RedactedValue)
			count = 1
		}
	}()
	return redactor.RedactBytes(value)
}

func redactValueSafely(
	redactor Redactor,
	value any,
) (clean any, count int) {
	clean = value
	defer func() {
		if recover() != nil {
			clean = RedactedValue
			count = 1
		}
	}()
	return redactor.RedactValue(value)
}

func isNilRedactor(redactor Redactor) bool {
	if redactor == nil {
		return true
	}
	value := reflect.ValueOf(redactor)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// NewRedactor returns a concurrency-safe redactor for common credential forms.
// RedactValue preserves a concrete type when it can do so safely. Values whose
// serialization hides detected secret material are replaced with RedactedValue.
func NewRedactor() Redactor {
	core := `(?:api[_-]?key|access[_-]?key|access[_-]?token|auth[_-]?token|authorization|client[_-]?secret|credential|passphrase|password|passwd|private[_-]?key|pwd|refresh[_-]?token|secret|token|aws[_-]?secret[_-]?access[_-]?key)`
	key := `(?:(?:[A-Za-z0-9]+)[_-])*` + core
	assignment := `(\b` + key + `\b"?\s*[:=]\s*)`
	flag := `(?i)(--` + key + `(?:=|\s+))`
	networkAuthFlag := `(?i)((?:^|\s)(?:-u|--user|--proxy-user)(?:=|\s+))`
	utilityFlag := `(?i)(\bsshpass\b[^\r\n]*?[ \t]-p(?:=|\s+))`
	phraseKey := `(?:(?:[A-Za-z0-9]+)[_-])*(?:passphrase|password|passwd|pwd)`
	phraseAssignment := `(\b` + phraseKey + `\b"?\s*[:=]\s*)`
	phraseValue := `[^\s,;}\]"':=]+(?:[ \t]+[A-Za-z0-9][^\s,;}\]"':=]*)+`
	unquoted := `(?:[^\s,;}\]"']*(?:\[REDACTED\][^\s,;}\]"']*)+|[^\s,;}\]"']+)`
	return &patternRedactor{
		patterns: []redactionPattern{
			newRedactionPattern(`(?s)-----BEGIN(?: [A-Z0-9]+)* PRIVATE KEY-----.*?-----END(?: [A-Z0-9]+)* PRIVATE KEY-----`, RedactedValue),
			newRedactionPattern(`(?s)-----BEGIN(?: [A-Z0-9]+)* PRIVATE KEY-----.*`, RedactedValue),
			newRedactionPattern(`(?im)(\bauthorization\b\s*[:=]\s*)(?:bearer|basic)\s+[^\s,;}\]"']+`, `${1}`+RedactedValue),
			newRedactionPattern(`(?i)(\b(?:bearer|basic)\s+)[A-Za-z0-9._~+/=-]{8,}`, `${1}`+RedactedValue),
			newRedactionPattern(`(?i)(\b[a-z][a-z0-9+.-]*://[^:/\s]+:)[^@\s/]+@`, `${1}`+RedactedValue+`@`),
			newRedactionPattern(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{8,}\b`, RedactedValue),
			newRedactionPattern(`\bsk-(?:proj-|svcacct-)?[A-Za-z0-9_-]{16,}\b`, RedactedValue),
			newRedactionPattern(`\bsk-ant-[A-Za-z0-9_-]{16,}\b`, RedactedValue),
			newRedactionPattern(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`, RedactedValue),
			newRedactionPattern(`\b(?:A3T|AKIA|ASIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA|ASCA)[A-Z0-9]{16}\b`, RedactedValue),
			newRedactionPattern(`\bAIza[0-9A-Za-z_-]{35}\b`, RedactedValue),
			newRedactionPattern(`\b(?:xox[baprs]-[A-Za-z0-9-]{10,}|glpat-[A-Za-z0-9_-]{16,}|npm_[A-Za-z0-9]{16,})\b`, RedactedValue),
			newRedactionPattern(`(?i)`+assignment+`\$?"[^"]*"`, `${1}"`+RedactedValue+`"`),
			newRedactionPattern(`(?i)`+assignment+`\$?'[^']*'`, `${1}'`+RedactedValue+`'`),
			newRedactionPattern(`(?i)`+phraseAssignment+phraseValue, `${1}`+RedactedValue),
			newRedactionPattern(`(?i)`+assignment+unquoted, `${1}`+RedactedValue),
			newRedactionPattern(flag+`\$?"[^"]*"`, `${1}"`+RedactedValue+`"`),
			newRedactionPattern(flag+`\$?'[^']*'`, `${1}'`+RedactedValue+`'`),
			newRedactionPattern(flag+unquoted, `${1}`+RedactedValue),
			newRedactionPattern(networkAuthFlag+`\$?"[^"]*"`, `${1}"`+RedactedValue+`"`),
			newRedactionPattern(networkAuthFlag+`\$?'[^']*'`, `${1}'`+RedactedValue+`'`),
			newRedactionPattern(networkAuthFlag+unquoted, `${1}`+RedactedValue),
			newRedactionPattern(utilityFlag+`\$?"[^"]*"`, `${1}"`+RedactedValue+`"`),
			newRedactionPattern(utilityFlag+`\$?'[^']*'`, `${1}'`+RedactedValue+`'`),
			newRedactionPattern(utilityFlag+unquoted, `${1}`+RedactedValue),
		},
		sensitiveKey: regexp.MustCompile(`(?i)^` + key + `$`),
	}
}

func newRedactionPattern(expression, replacement string) redactionPattern {
	return redactionPattern{
		expression:  regexp.MustCompile(expression),
		replacement: replacement,
	}
}

func (r *patternRedactor) RedactString(value string) (string, int) {
	if r == nil || value == "" {
		return value, 0
	}
	redacted := value
	count := 0
	for _, pattern := range r.patterns {
		clean, found := applyRedactionPattern(redacted, pattern)
		redacted = clean
		count += found
	}
	return redacted, count
}

func applyRedactionPattern(value string, pattern redactionPattern) (string, int) {
	matches := pattern.expression.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value, 0
	}
	var builder strings.Builder
	builder.Grow(len(value))
	last := 0
	count := 0
	for _, match := range matches {
		replacement := pattern.expression.ExpandString(
			nil,
			pattern.replacement,
			value,
			match,
		)
		builder.WriteString(value[last:match[0]])
		_, _ = builder.Write(replacement)
		if value[match[0]:match[1]] != string(replacement) {
			count++
		}
		last = match[1]
	}
	builder.WriteString(value[last:])
	return builder.String(), count
}

func (r *patternRedactor) RedactBytes(value []byte) ([]byte, int) {
	if value == nil {
		return nil, 0
	}
	redacted, count := r.RedactString(string(value))
	return []byte(redacted), count
}

func (r *patternRedactor) RedactValue(value any) (any, int) {
	if r == nil {
		return value, 0
	}
	return r.redactValue(value)
}

func (r *patternRedactor) redactValue(value any) (any, int) {
	switch typed := value.(type) {
	case nil:
		return nil, 0
	case string:
		return r.RedactString(typed)
	case json.RawMessage:
		return r.redactRawMessage(typed)
	case []byte:
		return r.RedactBytes(typed)
	case map[string]any:
		return r.redactAnyMap(typed)
	case map[string]string:
		return r.redactStringMap(typed)
	case []any:
		return r.redactAnySlice(typed)
	case []string:
		return r.redactStringSlice(typed)
	case error:
		clean, count := r.RedactString(typed.Error())
		if count == 0 && !r.hasReflectiveSecret(value) {
			return value, 0
		}
		if count == 0 {
			return errors.New("tool safety: error details " + RedactedValue), 1
		}
		return errors.New(clean), count
	case fmt.Stringer:
		clean, count := r.redactJSONValue(value)
		cleanStringer, ok := clean.(fmt.Stringer)
		if !ok {
			return clean, count
		}
		display, displayCount := r.redactStringer(clean, cleanStringer)
		if displayCount > 0 {
			return display, count + displayCount
		}
		return clean, count
	default:
		return r.redactJSONValue(value)
	}
}
func (r *patternRedactor) redactStringer(
	value any,
	stringer fmt.Stringer,
) (result any, count int) {
	result = value
	defer func() {
		if recover() != nil {
			result = RedactedValue
			count = 1
		}
	}()
	clean, count := r.RedactString(stringer.String())
	if count == 0 {
		return value, 0
	}
	return clean, count
}

func (r *patternRedactor) redactJSONValue(value any) (any, int) {
	hasReflectiveSecret := r.hasReflectiveSecret(value)
	valueType := reflect.TypeOf(value)
	if valueType == nil {
		return nil, 0
	}
	switch valueType.Kind() {
	case reflect.String:
		clean, count := r.RedactString(reflect.ValueOf(value).String())
		if count == 0 {
			return value, 0
		}
		target := reflect.New(valueType).Elem()
		target.SetString(clean)
		return target.Interface(), count
	case reflect.Slice:
		if valueType.Elem() == reflect.TypeOf(byte(0)) {
			clean, count := r.RedactBytes(reflect.ValueOf(value).Bytes())
			if count == 0 {
				return value, 0
			}
			target := reflect.MakeSlice(valueType, len(clean), len(clean))
			reflect.Copy(target, reflect.ValueOf(clean))
			return target.Interface(), count
		}
	case reflect.Array, reflect.Map, reflect.Pointer, reflect.Struct:
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Uintptr, reflect.Float32, reflect.Float64:
		return value, 0
	default:
		return RedactedValue, 1
	}
	encoded, err := marshalJSONSafely(value)
	if err != nil {
		return RedactedValue, 1
	}
	redacted, count := r.redactRawMessage(json.RawMessage(encoded))
	if count == 0 {
		if hasReflectiveSecret {
			return RedactedValue, 1
		}
		return value, 0
	}
	raw, ok := redacted.(json.RawMessage)
	if !ok {
		return redacted, count
	}
	target := reflect.New(valueType)
	if valueType.Kind() == reflect.Pointer {
		target = reflect.New(valueType.Elem())
	}
	if err := unmarshalJSONSafely(raw, target.Interface()); err != nil {
		return raw, count
	}
	var result any
	if valueType.Kind() == reflect.Pointer {
		result = target.Interface()
	} else {
		result = target.Elem().Interface()
	}
	if r.hasReflectiveSecret(result) {
		return RedactedValue, count + 1
	}
	return result, count
}

type reflectiveVisit struct {
	typeOf  reflect.Type
	pointer uintptr
	length  int
}

type reflectiveSecretInspector struct {
	redactor *patternRedactor
	visited  map[reflectiveVisit]struct{}
	nodes    int
	bytes    int
}

func (r *patternRedactor) hasReflectiveSecret(value any) (found bool) {
	defer func() {
		if recover() != nil {
			found = true
		}
	}()
	inspector := reflectiveSecretInspector{
		redactor: r,
		visited:  make(map[reflectiveVisit]struct{}),
	}
	return inspector.inspect(reflect.ValueOf(value), 0)
}

func (i *reflectiveSecretInspector) inspect(value reflect.Value, depth int) bool {
	if !value.IsValid() {
		return false
	}
	i.nodes++
	if depth > maxReflectiveInspectionDepth ||
		i.nodes > maxReflectiveInspectionNodes {
		return true
	}
	value, ok := unwrapReflectiveValue(value)
	if !ok || i.wasVisited(value) {
		return false
	}

	switch value.Kind() {
	case reflect.Pointer:
		return i.inspect(value.Elem(), depth+1)
	case reflect.String:
		return i.inspectString(value.String())
	case reflect.Slice:
		return i.inspectSlice(value, depth)
	case reflect.Array:
		return i.inspectSequence(value, depth)
	case reflect.Map:
		return i.inspectMap(value, depth)
	case reflect.Struct:
		return i.inspectStruct(value, depth)
	default:
		return false
	}
}

func unwrapReflectiveValue(value reflect.Value) (reflect.Value, bool) {
	for value.Kind() == reflect.Interface {
		if value.IsNil() {
			return reflect.Value{}, false
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice:
		if value.IsNil() {
			return reflect.Value{}, false
		}
	}
	return value, true
}

func (i *reflectiveSecretInspector) wasVisited(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice:
		visit := reflectiveVisit{typeOf: value.Type(), pointer: value.Pointer()}
		if value.Kind() == reflect.Slice {
			visit.length = value.Len()
		}
		if _, ok := i.visited[visit]; ok {
			return true
		}
		i.visited[visit] = struct{}{}
	}
	return false
}

func (i *reflectiveSecretInspector) inspectString(value string) bool {
	i.bytes += len(value)
	if i.bytes > maxReflectiveInspectionBytes {
		return true
	}
	_, count := i.redactor.RedactString(value)
	return count > 0
}

func (i *reflectiveSecretInspector) inspectSlice(
	value reflect.Value,
	depth int,
) bool {
	if value.Type().Elem().Kind() != reflect.Uint8 {
		return i.inspectSequence(value, depth)
	}
	i.bytes += value.Len()
	if i.bytes > maxReflectiveInspectionBytes {
		return true
	}
	_, count := i.redactor.RedactBytes(value.Bytes())
	return count > 0
}

func (i *reflectiveSecretInspector) inspectMap(
	value reflect.Value,
	depth int,
) bool {
	iterator := value.MapRange()
	for iterator.Next() {
		key := iterator.Key()
		item := iterator.Value()
		if key.Kind() == reflect.String &&
			i.redactor.isSensitiveKey(key.String()) &&
			isNonEmptySensitiveValue(item) {
			return true
		}
		if i.inspect(key, depth+1) || i.inspect(item, depth+1) {
			return true
		}
	}
	return false
}

func (i *reflectiveSecretInspector) inspectStruct(
	value reflect.Value,
	depth int,
) bool {
	typeOf := value.Type()
	for index := 0; index < value.NumField(); index++ {
		field := typeOf.Field(index)
		item := value.Field(index)
		if (i.redactor.isSensitiveKey(field.Name) ||
			i.redactor.isSensitiveKey(normalizeGoFieldName(field.Name)) ||
			i.redactor.isSensitiveKey(jsonFieldName(field))) &&
			isNonEmptySensitiveValue(item) {
			return true
		}
		if i.inspect(item, depth+1) {
			return true
		}
	}
	return false
}

func (i *reflectiveSecretInspector) inspectSequence(
	value reflect.Value,
	depth int,
) bool {
	for index := 0; index < value.Len(); index++ {
		if i.inspect(value.Index(index), depth+1) {
			return true
		}
	}
	return false
}

func isNonEmptySensitiveValue(value reflect.Value) bool {
	for value.IsValid() &&
		(value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.IsZero() {
		return false
	}
	return value.Kind() != reflect.String || value.String() != RedactedValue
}

func normalizeGoFieldName(name string) string {
	runes := []rune(name)
	var builder strings.Builder
	for index, current := range runes {
		if index > 0 && unicode.IsUpper(current) &&
			(unicode.IsLower(runes[index-1]) ||
				unicode.IsDigit(runes[index-1]) ||
				(index+1 < len(runes) && unicode.IsLower(runes[index+1]))) {
			builder.WriteByte('_')
		}
		builder.WriteRune(unicode.ToLower(current))
	}
	return builder.String()
}

func jsonFieldName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	if name == "-" {
		return ""
	}
	return name
}

func marshalJSONSafely(value any) (encoded []byte, err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("marshal JSON panicked")
		}
	}()
	return json.Marshal(value)
}

func unmarshalJSONSafely(value []byte, target any) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("unmarshal JSON panicked")
		}
	}()
	return json.Unmarshal(value, target)
}

func (r *patternRedactor) redactRawMessage(value json.RawMessage) (any, int) {
	var decoded any
	if err := unmarshalJSONSafely(value, &decoded); err != nil {
		redacted, count := r.RedactBytes(value)
		return json.RawMessage(redacted), count
	}
	redacted, count := r.redactValue(decoded)
	encoded, err := marshalJSONSafely(redacted)
	if err != nil {
		fallback, fallbackCount := r.RedactBytes(value)
		return json.RawMessage(fallback), count + fallbackCount
	}
	return json.RawMessage(encoded), count
}

func (r *patternRedactor) redactAnyMap(value map[string]any) (any, int) {
	redacted := make(map[string]any, len(value))
	count := 0
	for key, item := range value {
		if r.isSensitiveKey(key) {
			redacted[key] = RedactedValue
			if item != RedactedValue {
				count++
			}
			continue
		}
		clean, itemCount := r.redactValue(item)
		redacted[key] = clean
		count += itemCount
	}
	return redacted, count
}

func (r *patternRedactor) redactStringMap(value map[string]string) (any, int) {
	redacted := make(map[string]string, len(value))
	count := 0
	for key, item := range value {
		if r.isSensitiveKey(key) {
			redacted[key] = RedactedValue
			if item != RedactedValue {
				count++
			}
			continue
		}
		clean, itemCount := r.RedactString(item)
		redacted[key] = clean
		count += itemCount
	}
	return redacted, count
}

func (r *patternRedactor) redactAnySlice(value []any) (any, int) {
	redacted := make([]any, len(value))
	count := 0
	for index, item := range value {
		clean, itemCount := r.redactValue(item)
		redacted[index] = clean
		count += itemCount
	}
	return redacted, count
}

func (r *patternRedactor) redactStringSlice(value []string) (any, int) {
	redacted := make([]string, len(value))
	count := 0
	for index, item := range value {
		clean, itemCount := r.RedactString(item)
		redacted[index] = clean
		count += itemCount
	}
	return redacted, count
}

func (r *patternRedactor) isSensitiveKey(key string) bool {
	normalized := strings.TrimSpace(key)
	if r.matchesSensitiveKey(normalized) {
		return true
	}
	for _, segment := range strings.FieldsFunc(normalized, func(r rune) bool {
		switch r {
		case '.', '/', '\\', ':', '[', ']':
			return true
		default:
			return false
		}
	}) {
		if r.matchesSensitiveKey(segment) {
			return true
		}
	}
	return false
}

func (r *patternRedactor) matchesSensitiveKey(key string) bool {
	return r.sensitiveKey.MatchString(key) ||
		r.sensitiveKey.MatchString(normalizeGoFieldName(key))
}
