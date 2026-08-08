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
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxInputBytes   = 1 << 20
	maxOutputBytes  = 64 << 10
	maxValueDepth   = 64
	maxValueItems   = 10000
	maxValueAliases = 1024

	maxValueProcessedBytes = 4 << 20
	maxValueOutputBytes    = 1 << 20

	inputTooLargeMarker = "[REDACTED:input-too-large]"
	truncatedMarker     = "\n...[TRUNCATED]...\n"
)

var (
	assignmentPattern = regexp.MustCompile(
		`(?im)(["']?)(\b` +
			`[A-Za-z0-9_.-]*(?:password|passwd|pwd|token|secret|credentials?)[A-Za-z0-9_.-]*|` +
			`[A-Za-z0-9_.-]*(?:api|access|openai|private)[_.-]?key[A-Za-z0-9_.-]*` +
			`)(["']?)([ \t]*(?:=|:)[ \t]*)` +
			`("(?:\\.|[^"\\\r\n])*"[^\s,;}\]]*|` +
			`'(?:\\.|[^'\\\r\n])*'[^\s,;}\]]*|\S+)`,
	)
	redactedValuePattern  = regexp.MustCompile(`^\[REDACTED:[A-Za-z0-9-]+\]$`)
	unquotedMarkerPattern = regexp.MustCompile(
		`(?im)\b(?:` +
			`[A-Za-z0-9_.-]*(?:password|passwd|pwd|token|secret|credentials?)[A-Za-z0-9_.-]*|` +
			`[A-Za-z0-9_.-]*(?:api|access|openai|private)[_.-]?key[A-Za-z0-9_.-]*` +
			`)[ \t]*(?:=|:)[ \t]*\[REDACTED:[A-Za-z0-9-]+\]`,
	)
	assignmentFieldBoundaryPattern = regexp.MustCompile(
		`(?im)[ \t]+["']?(\b` +
			`[A-Za-z0-9_.-]*(?:password|passwd|pwd|token|secret|credentials?)[A-Za-z0-9_.-]*|` +
			`[A-Za-z0-9_.-]*(?:api|access|openai|private)[_.-]?key[A-Za-z0-9_.-]*` +
			`)["']?[ \t]*(?:=|:)`,
	)
	assignmentPrefixPattern = regexp.MustCompile(
		`(?im)(["']?)(\b` +
			`[A-Za-z0-9_.-]*(?:password|passwd|pwd|token|secret|credentials?)[A-Za-z0-9_.-]*|` +
			`[A-Za-z0-9_.-]*(?:api|access|openai|private)[_.-]?key[A-Za-z0-9_.-]*` +
			`)(["']?)([ \t]*(?:=|:)[ \t]*)`,
	)
	quotedMarkerPattern = regexp.MustCompile(
		`(?im)["']?\b(?:` +
			`[A-Za-z0-9_.-]*(?:password|passwd|pwd|token|secret|credentials?)[A-Za-z0-9_.-]*|` +
			`[A-Za-z0-9_.-]*(?:api|access|openai|private)[_.-]?key[A-Za-z0-9_.-]*` +
			`)["']?[ \t]*(?:=|:)[ \t]*(?:` +
			`"\[REDACTED:[A-Za-z0-9-]+\]"|` +
			`'\[REDACTED:[A-Za-z0-9-]+\]')`,
	)
	jsonPropertyBoundaryPattern = regexp.MustCompile(
		`,[ \t]*["']?[A-Za-z_][A-Za-z0-9_.-]*["']?[ \t]*:`,
	)
	bearerBoundaryPattern = regexp.MustCompile(
		`(?im)[ \t]+bearer[ \t]+\[REDACTED:bearer-token\]`,
	)
	jsonNumberType = reflect.TypeOf(json.Number(""))
	dsnURLPattern  = regexp.MustCompile(
		`(?i)\b(postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|rediss|amqp|amqps)` +
			`://([^:/@\s]+):([^@\s/]+)@`,
	)
	mysqlDSNPattern = regexp.MustCompile(
		`(?i)\b([^:\s/@]+):([^@\s]+)@tcp\(`,
	)
	credentialURLPattern = regexp.MustCompile(
		`(?i)\b(https?|ftps?)://([^:/@\s]+):([^@\s/]+)@`,
	)
	authorizationHeaderPattern = regexp.MustCompile(
		`(?im)(\bauthorization[ \t]*:[ \t]*)([^\r\n]*)`,
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
	value = strings.ToValidUTF8(value, "\uFFFD")

	redacted := redactLogicalAssignmentContinuations(value)
	redacted = redactPrivateKeys(redacted)
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
	redacted = authorizationHeaderPattern.ReplaceAllStringFunc(
		redacted,
		redactAuthorizationHeader,
	)
	redacted = standaloneBearerPattern.ReplaceAllString(
		redacted,
		`${1}[REDACTED:bearer-token]`,
	)
	redacted = openAIKeyPattern.ReplaceAllString(
		redacted,
		"[REDACTED:openai-api-key]",
	)
	redacted = redactUnquotedAssignmentTails(redacted)
	redacted = redactQuotedAssignmentTails(redacted)
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

// Value returns a redacted copy without mutating value. It supports scalar
// values, string-keyed maps, slices, arrays, interfaces, and pointers to those
// values. Sensitive map fields use a marker when the destination type accepts
// one and otherwise use the type's zero value. Map keys are sanitized in
// lexical input-key order; collisions retain every entry by adding a
// deterministic " [COLLISION:n]" suffix. Value preserves aliases and rejects
// cycles or inputs that exceed its traversal limits.
func Value(value any) (any, error) {
	if value == nil {
		return nil, nil
	}

	state := valueState{
		active: make(map[valueVisit]bool),
		memo:   make(map[valueVisit]reflect.Value),
	}
	items := 0
	redacted, err := redactValue(reflect.ValueOf(value), 0, &items, &state)
	if err != nil {
		return nil, fmt.Errorf("redact value: %w", err)
	}
	return redacted.Interface(), nil
}

func redactAssignment(match string) string {
	parts := assignmentPattern.FindStringSubmatch(match)
	if len(parts) != 6 {
		return match
	}
	kind, ok := sensitiveKind(parts[2])
	if !ok {
		return match
	}
	if isRedactedValue(parts[5]) {
		return match
	}
	return parts[1] + parts[2] + parts[3] + parts[4] +
		replaceValue(parts[5], kind)
}

func redactAuthorizationHeader(match string) string {
	parts := authorizationHeaderPattern.FindStringSubmatch(match)
	if len(parts) != 3 {
		return match
	}
	fields := strings.Fields(parts[2])
	if len(fields) > 0 && strings.EqualFold(fields[0], "bearer") {
		return parts[1] + fields[0] + " [REDACTED:bearer-token]"
	}
	return parts[1] + "[REDACTED:authorization]"
}

type logicalAssignment struct {
	valueStart int
	kind       string
	quote      byte
}

func redactLogicalAssignmentContinuations(value string) string {
	var out strings.Builder
	written := 0
	for lineStart := 0; lineStart < len(value); {
		lineEnd, nextLine := lineBounds(value, lineStart)
		line := value[lineStart:lineEnd]
		assignment, ok := continuedAssignment(line)
		if !ok {
			lineStart = nextLine
			continue
		}

		finalEnd, finalNext := continuationEnd(
			value,
			lineEnd,
			nextLine,
			assignment.quote,
		)
		out.WriteString(value[written : lineStart+assignment.valueStart])
		marker := "[REDACTED:" + assignment.kind + "]"
		if assignment.quote != 0 {
			out.WriteByte(assignment.quote)
			out.WriteString(marker)
			out.WriteByte(assignment.quote)
		} else {
			out.WriteString(marker)
		}
		out.WriteString(value[finalEnd:finalNext])
		written = finalNext
		lineStart = finalNext
	}
	if written == 0 {
		return value
	}
	out.WriteString(value[written:])
	return out.String()
}

func continuedAssignment(line string) (logicalAssignment, bool) {
	for _, match := range assignmentPrefixPattern.FindAllStringSubmatchIndex(line, -1) {
		name := line[match[4]:match[5]]
		kind, sensitive := sensitiveKind(name)
		if !sensitive || match[1] >= len(line) {
			continue
		}
		valueStart := match[1]
		value := line[valueStart:]
		if value[0] == '\'' || value[0] == '"' {
			if findUnescapedQuote(value, value[0], 1) < 0 {
				return logicalAssignment{
					valueStart: valueStart,
					kind:       kind,
					quote:      value[0],
				}, true
			}
			continue
		}
		if strings.HasSuffix(strings.TrimSpace(value), "\\") {
			return logicalAssignment{valueStart: valueStart, kind: kind}, true
		}
	}
	return logicalAssignment{}, false
}

func continuationEnd(
	value string,
	currentEnd int,
	nextLine int,
	quote byte,
) (int, int) {
	if nextLine >= len(value) {
		return currentEnd, nextLine
	}
	for lineStart := nextLine; lineStart < len(value); {
		lineEnd, followingLine := lineBounds(value, lineStart)
		line := value[lineStart:lineEnd]
		if quote != 0 {
			if findUnescapedQuote(line, quote, 0) >= 0 {
				return lineEnd, followingLine
			}
		} else if !strings.HasSuffix(strings.TrimSpace(line), "\\") {
			return lineEnd, followingLine
		}
		lineStart = followingLine
	}
	return len(value), len(value)
}

func findUnescapedQuote(value string, quote byte, start int) int {
	escaped := false
	for i := start; i < len(value); i++ {
		if escaped {
			escaped = false
			continue
		}
		if value[i] == '\\' {
			escaped = true
			continue
		}
		if value[i] == quote {
			return i
		}
	}
	return -1
}

func sensitiveKind(name string) (string, bool) {
	if !isIdentifier(name) {
		return "", false
	}
	return sensitiveKindParts(identifierParts(name))
}

func sensitiveMapKeyKind(name string) (string, bool) {
	return sensitiveKindParts(identifierParts(strings.TrimSpace(name)))
}

func isMetadataName(name string) bool {
	return isIdentifier(name) && isMetadataIdentifier(identifierParts(name))
}

func sensitiveKindParts(parts []string) (string, bool) {
	if len(parts) == 0 {
		return "", false
	}
	compact := strings.Join(parts, "")
	if isMetadataIdentifier(parts) {
		return "", false
	}
	if strings.HasSuffix(compact, "privatekey") {
		return "private-key", true
	}
	for _, suffix := range []string{"apikey", "accesskey", "secretkey", "openaikey"} {
		if strings.HasSuffix(compact, suffix) {
			return "api-key", true
		}
	}
	if strings.HasSuffix(compact, "password") ||
		strings.HasSuffix(compact, "passwd") ||
		strings.HasSuffix(compact, "pwd") {
		return "password", true
	}
	if strings.HasSuffix(compact, "token") {
		return "token", true
	}
	if strings.HasSuffix(compact, "secret") {
		return "secret", true
	}
	if strings.HasSuffix(compact, "credential") ||
		strings.HasSuffix(compact, "credentials") {
		return "credential", true
	}

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

func isMetadataIdentifier(parts []string) bool {
	metadata := false
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		switch part {
		case "token":
			if i+1 < len(parts) && isTokenMetadataPart(parts[i+1]) {
				metadata = true
				continue
			}
			return false
		case "password", "passwd", "pwd":
			if i+1 < len(parts) && parts[i+1] == "policy" {
				metadata = true
				continue
			}
			return false
		case "api", "access", "openai":
			if i+1 < len(parts) && parts[i+1] == "key" {
				if i+2 < len(parts) && parts[i+2] == "count" {
					metadata = true
					i += 2
					continue
				}
				return false
			}
		case "private":
			if i+1 < len(parts) && parts[i+1] == "key" {
				if i+2 < len(parts) && parts[i+2] == "path" {
					metadata = true
					i += 2
					continue
				}
				return false
			}
		case "secret":
			if i+1 < len(parts) && parts[i+1] == "count" {
				metadata = true
				i++
				continue
			}
			return false
		case "credential", "credentials":
			if i+1 < len(parts) && parts[i+1] == "type" {
				metadata = true
				i++
				continue
			}
			return false
		case "key":
			if i > 0 {
				switch parts[i-1] {
				case "api", "access", "openai", "private", "secret":
					return false
				}
			}
		}
	}
	return metadata
}

func isTokenMetadataPart(part string) bool {
	switch part {
	case "count", "endpoint", "expiration", "expiry", "expires", "lifetime",
		"limit", "policy", "scope", "ttl", "type", "url", "usage":
		return true
	default:
		return false
	}
}

func isIdentifier(name string) bool {
	for i, r := range name {
		if unicode.IsLetter(r) || r == '_' || r == '-' || r == '.' ||
			i > 0 && unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return name != ""
}

func identifierParts(name string) []string {
	runes := []rune(name)
	parts := make([]string, 0, 4)
	start := 0
	appendPart := func(end int) {
		if start < end {
			parts = append(parts, strings.ToLower(string(runes[start:end])))
		}
	}
	for i, r := range runes {
		if r == '_' || r == '-' || !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			appendPart(i)
			start = i + 1
			continue
		}
		if i == start || !unicode.IsUpper(r) {
			continue
		}
		previous := runes[i-1]
		nextIsLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
		if unicode.IsLower(previous) || unicode.IsDigit(previous) ||
			unicode.IsUpper(previous) && nextIsLower {
			appendPart(i)
			start = i
		}
	}
	appendPart(len(runes))
	return parts
}

func isRedactedValue(value string) bool {
	value = strings.Trim(value, `"'`)
	return redactedValuePattern.MatchString(value)
}

func replaceValue(value, kind string) string {
	marker := "[REDACTED:" + kind + "]"
	if len(value) > 0 && (value[0] == '\'' || value[0] == '"') {
		if value[len(value)-1] == value[0] {
			return value[:1] + marker + value[:1]
		}
		return marker
	}
	return marker
}

func redactUnquotedAssignmentTails(value string) string {
	var out strings.Builder
	for lineStart := 0; lineStart < len(value); {
		lineEnd, nextLine := lineBounds(value, lineStart)
		out.WriteString(redactUnquotedAssignmentLine(value[lineStart:lineEnd]))
		out.WriteString(value[lineEnd:nextLine])
		lineStart = nextLine
	}
	return out.String()
}

func redactQuotedAssignmentTails(value string) string {
	var out strings.Builder
	for lineStart := 0; lineStart < len(value); {
		lineEnd, nextLine := lineBounds(value, lineStart)
		out.WriteString(redactQuotedAssignmentLine(value[lineStart:lineEnd]))
		out.WriteString(value[lineEnd:nextLine])
		lineStart = nextLine
	}
	return out.String()
}

func redactQuotedAssignmentLine(line string) string {
	markers := quotedMarkerPattern.FindAllStringIndex(line, -1)
	if len(markers) == 0 {
		return line
	}
	boundaries := quotedAssignmentBoundaries(line)

	var out strings.Builder
	cursor := 0
	boundaryIndex := 0
	for _, marker := range markers {
		if marker[0] < cursor {
			continue
		}
		out.WriteString(line[cursor:marker[1]])
		for boundaryIndex < len(boundaries) && boundaries[boundaryIndex] < marker[1] {
			boundaryIndex++
		}
		if boundaryIndex == len(boundaries) {
			tail := line[marker[1]:]
			if safeQuotedTail(tail) {
				out.WriteString(tail)
			}
			return out.String()
		}
		cursor = boundaries[boundaryIndex]
	}
	out.WriteString(line[cursor:])
	return out.String()
}

func quotedAssignmentBoundaries(line string) []int {
	boundaries := assignmentBoundaries(line)
	for _, match := range jsonPropertyBoundaryPattern.FindAllStringIndex(line, -1) {
		boundaries = append(boundaries, match[0])
	}
	sort.Ints(boundaries)
	return boundaries
}

func safeQuotedTail(tail string) bool {
	tail = strings.TrimSpace(tail)
	if tail == "" {
		return true
	}
	for _, r := range tail {
		if r != '}' && r != ']' {
			return false
		}
	}
	return true
}

func redactUnquotedAssignmentLine(line string) string {
	markers := unquotedMarkerPattern.FindAllStringIndex(line, -1)
	if len(markers) == 0 {
		return line
	}
	boundaries := assignmentBoundaries(line)

	var out strings.Builder
	cursor := 0
	boundaryIndex := 0
	for _, marker := range markers {
		if marker[0] < cursor {
			continue
		}
		out.WriteString(line[cursor:marker[1]])
		for boundaryIndex < len(boundaries) && boundaries[boundaryIndex] < marker[1] {
			boundaryIndex++
		}
		if boundaryIndex == len(boundaries) {
			if tail := line[marker[1]:]; strings.TrimSpace(tail) == "" {
				out.WriteString(tail)
			}
			return out.String()
		}
		cursor = boundaries[boundaryIndex]
	}
	out.WriteString(line[cursor:])
	return out.String()
}

func assignmentBoundaries(line string) []int {
	boundaries := make([]int, 0)
	for _, match := range assignmentFieldBoundaryPattern.FindAllStringSubmatchIndex(line, -1) {
		name := line[match[2]:match[3]]
		if _, sensitive := sensitiveKind(name); sensitive || isMetadataName(name) {
			boundaries = append(boundaries, match[0])
		}
	}
	for _, match := range bearerBoundaryPattern.FindAllStringIndex(line, -1) {
		boundaries = append(boundaries, match[0])
	}
	sort.Ints(boundaries)
	return boundaries
}

func redactPrivateKeys(value string) string {
	var out strings.Builder
	written := 0
	for lineStart := 0; lineStart < len(value); {
		lineEnd, nextLine := lineBounds(value, lineStart)
		line := value[lineStart:lineEnd]
		label, beginStart, _, ok := findPEMBoundary(line, "BEGIN")
		if !ok || !isPrivateKeyLabel(label) {
			lineStart = nextLine
			continue
		}
		redactionStart := lineStart + beginStart
		if strings.TrimSpace(line[:beginStart]) == "" {
			redactionStart = lineStart
		}

		redactionEnd := len(value)
		for footerStart := nextLine; footerStart < len(value); {
			footerEnd, footerNext := lineBounds(value, footerStart)
			footerLabel, _, boundaryEnd, footerOK := findPEMBoundary(
				value[footerStart:footerEnd], "END",
			)
			if footerOK && footerLabel == label {
				redactionEnd = footerStart + boundaryEnd
				break
			}
			footerStart = footerNext
		}

		out.WriteString(value[written:redactionStart])
		out.WriteString("[REDACTED:private-key]")
		written = redactionEnd
		lineStart = redactionEnd
	}
	if written == 0 {
		return value
	}
	out.WriteString(value[written:])
	return out.String()
}

func lineBounds(value string, start int) (end, next int) {
	offset := strings.IndexByte(value[start:], '\n')
	if offset < 0 {
		end = len(value)
		next = len(value)
	} else {
		end = start + offset
		next = end + 1
	}
	if end > start && value[end-1] == '\r' {
		end--
	}
	return end, next
}

func findPEMBoundary(
	line string,
	boundary string,
) (label string, start int, end int, ok bool) {
	prefix := "-----" + boundary + " "
	searchStart := 0
	for searchStart < len(line) {
		offset := strings.Index(line[searchStart:], prefix)
		if offset < 0 {
			return "", 0, 0, false
		}
		start = searchStart + offset
		labelStart := start + len(prefix)
		labelEndOffset := strings.Index(line[labelStart:], "-----")
		if labelEndOffset < 0 {
			return "", 0, 0, false
		}
		labelEnd := labelStart + labelEndOffset
		label = line[labelStart:labelEnd]
		end = labelEnd + len("-----")
		if validPEMLabel(label) &&
			(boundary != "END" || strings.TrimSpace(line[end:]) == "") {
			return label, start, end, true
		}
		searchStart = start + len(prefix)
	}
	return "", 0, 0, false
}

func validPEMLabel(label string) bool {
	if len(label) == 0 || len(label) > 64 {
		return false
	}
	for _, r := range label {
		if r == ' ' || r == '-' || unicode.IsDigit(r) || r >= 'A' && r <= 'Z' {
			continue
		}
		return false
	}
	return true
}

func isPrivateKeyLabel(label string) bool {
	return label == "PRIVATE KEY" || strings.HasSuffix(label, " PRIVATE KEY")
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

type valueVisit struct {
	typeOf reflect.Type
	kind   reflect.Kind
	ptr    uintptr
	len    int
	cap    int
}

type valueState struct {
	aliases        int
	processedBytes int
	outputBytes    int
	active         map[valueVisit]bool
	memo           map[valueVisit]reflect.Value
}

func redactValue(
	value reflect.Value,
	depth int,
	items *int,
	state *valueState,
) (reflect.Value, error) {
	if depth > maxValueDepth {
		return reflect.Value{}, errors.New("maximum depth exceeded")
	}
	if err := takeValueItem(items); err != nil {
		return reflect.Value{}, errors.New("maximum item count exceeded")
	}

	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		item, err := redactValue(value.Elem(), depth+1, items, state)
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(value.Type()).Elem()
		out.Set(item)
		return out, nil
	case reflect.String:
		var redacted string
		var err error
		if value.Type() == jsonNumberType {
			redacted, err = redactJSONNumber(value.String(), state)
		} else {
			redacted, err = redactValueString(value.String(), state)
		}
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(value.Type()).Elem()
		out.SetString(redacted)
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
		visit := valueVisit{typeOf: value.Type(), kind: value.Kind(), ptr: value.Pointer()}
		if state.active[visit] {
			return reflect.Value{}, errors.New("cycle detected")
		}
		if out, ok, err := memoizedValue(state, visit); ok || err != nil {
			return out, err
		}
		if err := checkContainerItemBudget(value.Len(), *items); err != nil {
			return reflect.Value{}, err
		}
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		state.memo[visit] = out
		state.active[visit] = true
		defer delete(state.active, visit)

		keys := value.MapKeys()
		sort.Slice(keys, func(i, j int) bool {
			return keys[i].String() < keys[j].String()
		})
		usedKeys := make(map[string]bool, len(keys))
		collisions := make(map[string]int)
		for _, key := range keys {
			rawKey := key.String()
			baseKey, keyErr := redactValueString(rawKey, state)
			if keyErr != nil {
				return reflect.Value{}, keyErr
			}
			redactedKey := uniqueMapKey(baseKey, usedKeys, collisions)
			if err := takeValueOutputBytes(
				state,
				len(redactedKey)-len(baseKey),
			); err != nil {
				return reflect.Value{}, err
			}
			outKey := reflect.New(value.Type().Key()).Elem()
			outKey.SetString(redactedKey)

			mapValue := value.MapIndex(key)
			kind, sensitive := sensitiveMapKeyKind(rawKey)
			var item reflect.Value
			var err error
			if sensitive {
				err = takeValueItem(items)
				if err != nil {
					return reflect.Value{}, err
				}
				item, err = redactSensitiveValue(mapValue, kind, state)
			} else {
				item, err = redactValue(mapValue, depth+1, items, state)
			}
			if err != nil {
				return reflect.Value{}, err
			}
			out.SetMapIndex(outKey, item)
		}
		return out, nil
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return reflect.Zero(value.Type()), nil
		}
		if value.Len() == 0 {
			return reflect.MakeSlice(value.Type(), 0, 0), nil
		}
		visit := valueVisit{
			typeOf: value.Type(),
			kind:   value.Kind(),
			ptr:    value.Pointer(),
			len:    value.Len(),
			cap:    value.Cap(),
		}
		if state.active[visit] {
			return reflect.Value{}, errors.New("cycle detected")
		}
		if out, ok, err := memoizedValue(state, visit); ok || err != nil {
			return out, err
		}
		if err := checkContainerItemBudget(value.Len(), *items); err != nil {
			return reflect.Value{}, err
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		state.memo[visit] = out
		state.active[visit] = true
		defer delete(state.active, visit)
		for i := 0; i < value.Len(); i++ {
			item, err := redactValue(value.Index(i), depth+1, items, state)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(item)
		}
		return out, nil
	case reflect.Array:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return reflect.Zero(value.Type()), nil
		}
		if err := checkContainerItemBudget(value.Len(), *items); err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			item, err := redactValue(value.Index(i), depth+1, items, state)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(item)
		}
		return out, nil
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		visit := valueVisit{typeOf: value.Type(), kind: value.Kind(), ptr: value.Pointer()}
		if state.active[visit] {
			return reflect.Value{}, errors.New("cycle detected")
		}
		if out, ok, err := memoizedValue(state, visit); ok || err != nil {
			return out, err
		}
		out := reflect.New(value.Type().Elem())
		state.memo[visit] = out
		state.active[visit] = true
		defer delete(state.active, visit)
		item, err := redactValue(value.Elem(), depth+1, items, state)
		if err != nil {
			return reflect.Value{}, err
		}
		out.Elem().Set(item)
		return out, nil
	case reflect.Invalid:
		return reflect.Value{}, nil
	default:
		return reflect.Value{}, fmt.Errorf("unsupported type %s", value.Type())
	}
}

func redactValueString(value string, state *valueState) (string, error) {
	if err := takeValueProcessedBytes(state, len(value)); err != nil {
		return "", err
	}
	if err := checkValueOutputCapacity(
		state,
		maxRedactedStringBytes(len(value)),
	); err != nil {
		return "", err
	}
	redacted := String(value)
	if err := takeValueOutputBytes(state, len(redacted)); err != nil {
		return "", err
	}
	return redacted, nil
}

func redactJSONNumber(value string, state *valueState) (string, error) {
	if err := takeValueProcessedBytes(state, len(value)); err != nil {
		return "", err
	}
	if err := checkValueOutputCapacity(state, len(value)); err != nil {
		return "", err
	}
	number := sanitizeJSONNumber(json.Number(value))
	if err := takeValueOutputBytes(state, len(number)); err != nil {
		return "", err
	}
	return string(number), nil
}

func sanitizeJSONNumber(number json.Number) json.Number {
	if _, err := json.Marshal(number); err != nil {
		return json.Number("0")
	}
	return number
}

func takeValueProcessedBytes(state *valueState, count int) error {
	if count > maxValueProcessedBytes-state.processedBytes {
		return errors.New("maximum processed byte count exceeded")
	}
	state.processedBytes += count
	return nil
}

func takeValueOutputBytes(state *valueState, count int) error {
	if err := checkValueOutputCapacity(state, count); err != nil {
		return err
	}
	state.outputBytes += count
	return nil
}

func checkValueOutputCapacity(state *valueState, count int) error {
	if count > maxValueOutputBytes-state.outputBytes {
		return errors.New("maximum output byte count exceeded")
	}
	return nil
}

func maxRedactedStringBytes(length int) int {
	if length == 0 {
		return 0
	}
	if length > maxInputBytes {
		return len(inputTooLargeMarker)
	}
	if length >= maxOutputBytes/8 {
		return maxOutputBytes
	}
	bound := length*8 + 64
	if bound > maxOutputBytes {
		return maxOutputBytes
	}
	return bound
}

func takeValueItem(items *int) error {
	if *items >= maxValueItems {
		return errors.New("maximum item count exceeded")
	}
	(*items)++
	return nil
}

func checkContainerItemBudget(length int, items int) error {
	if length > maxValueItems-items {
		return errors.New("maximum item count exceeded")
	}
	return nil
}

func memoizedValue(
	state *valueState,
	visit valueVisit,
) (reflect.Value, bool, error) {
	out, ok := state.memo[visit]
	if !ok {
		return reflect.Value{}, false, nil
	}
	state.aliases++
	if state.aliases > maxValueAliases {
		return reflect.Value{}, true, errors.New("maximum alias count exceeded")
	}
	return out, true, nil
}

func uniqueMapKey(base string, used map[string]bool, collisions map[string]int) string {
	if !used[base] {
		used[base] = true
		collisions[base] = 1
		return base
	}
	for {
		collisions[base]++
		candidate := fmt.Sprintf("%s [COLLISION:%d]", base, collisions[base])
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

func redactSensitiveValue(
	value reflect.Value,
	kind string,
	state *valueState,
) (reflect.Value, error) {
	marker := "[REDACTED:" + kind + "]"
	switch value.Kind() {
	case reflect.Interface:
		out := reflect.New(value.Type()).Elem()
		markerValue := reflect.ValueOf(marker)
		if markerValue.Type().AssignableTo(value.Type()) {
			if err := takeValueOutputBytes(state, len(marker)); err != nil {
				return reflect.Value{}, err
			}
			out.Set(markerValue)
		}
		return out, nil
	case reflect.String:
		redacted := marker
		if value.Type() == jsonNumberType {
			redacted = "0"
		}
		if err := takeValueOutputBytes(state, len(redacted)); err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(value.Type()).Elem()
		out.SetString(redacted)
		return out, nil
	default:
		return reflect.Zero(value.Type()), nil
	}
}
