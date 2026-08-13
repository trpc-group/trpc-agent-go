//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	internalredact "trpc.group/trpc-go/trpc-agent-go/internal/redact"
)

const (
	resultAuditStage    = "post_execute"
	maxResultByteDepth  = 100
	binaryResultOmitted = "[binary result omitted]"

	minimumProcessedResultJSON = `{"value":null,"redacted":false,"truncated":true}`
)

// ProcessedResult is a JSON-safe, secret-minimized copy of a tool result and
// its execution error. ExecutionError carries the tool error as data; a Go
// error returned by Process instead reports processor, context, or required
// audit failures. The zero value contains no caller result or error data.
type ProcessedResult struct {
	Value          any    `json:"value"`
	ExecutionError string `json:"execution_error,omitempty"`
	Redacted       bool   `json:"redacted"`
	Truncated      bool   `json:"truncated"`
}

// ResultOption configures a ResultProcessor. NewResultProcessor ignores nil
// options and validates the resulting configuration before returning it.
type ResultOption func(*ResultProcessor)

// ResultProcessor explicitly sanitizes a final tool result after execution
// and after the caller's normal callbacks. It is not installed into framework
// callbacks automatically. It does not mutate caller-owned results and does
// not own or close its AuditSink. Concurrent use is safe when the configured
// sink is safe for concurrent use; JSONLAuditSink satisfies that requirement.
// Its zero value fails safely without returning caller result or error data.
type ResultProcessor struct {
	maxOutputBytes   int64
	auditSink        AuditSink
	auditFailureMode AuditFailureMode
}

// NewResultProcessor returns an explicit final-result processor using a copy
// of guard's maximum output limit. Guard must be non-nil and its configured
// limit must be positive and large enough to serialize the stable minimal safe
// result returned on truncation failure. The default audit mode is
// AuditBestEffort. A nil sink disables best-effort post-execution audit writes
// and is rejected in AuditRequired mode. Nil options are ignored. The caller
// retains ownership of guard and sink.
func NewResultProcessor(
	guard *Guard,
	sink AuditSink,
	opts ...ResultOption,
) (*ResultProcessor, error) {
	if guard == nil {
		return nil, errors.New("tool safety result processor guard is nil")
	}
	processor := &ResultProcessor{
		maxOutputBytes:   guard.policy.MaxOutputBytes,
		auditSink:        sink,
		auditFailureMode: AuditBestEffort,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(processor)
		}
	}
	if processor.maxOutputBytes <= 0 {
		return nil, errors.New("tool safety result processor output limit must be positive")
	}
	if processor.maxOutputBytes < int64(len(minimumProcessedResultJSON)) {
		return nil, fmt.Errorf(
			"tool safety result processor output limit must be at least %d bytes",
			len(minimumProcessedResultJSON),
		)
	}
	if !validAuditFailureMode(processor.auditFailureMode) {
		return nil, errors.New("tool safety result processor audit failure mode is invalid")
	}
	if processor.auditFailureMode == AuditRequired && processor.auditSink == nil {
		return nil, errors.New("required tool safety audit sink is nil")
	}
	return processor, nil
}

// WithResultAuditFailureMode configures post-execution audit handling.
// AuditBestEffort returns a safe processed value when the sink fails;
// AuditRequired returns that value together with an error preserving the sink
// cause and requires a non-nil sink. Unsupported modes cause
// NewResultProcessor to return an error.
func WithResultAuditFailureMode(mode AuditFailureMode) ResultOption {
	return func(processor *ResultProcessor) {
		if processor != nil {
			processor.auditFailureMode = mode
		}
	}
}

// Process copies result through JSON, recursively redacts sensitive data,
// converts executionErr to redacted single-line data, and enforces the guard's
// byte limit over the complete serialized ProcessedResult. Byte slices are
// inspected before JSON base64 encoding; sensitive text is redacted and binary
// data is replaced with an omission marker. Callers invoke Process explicitly
// after execution and their normal callbacks. A nil context is treated as
// context.Background. Invalid preflight data, cancellation, unsupported,
// cyclic, excessively nested, or ambiguously encoded result values, a nil or
// zero receiver, an output limit too small for the safe omission marker, or a
// required audit failure returns a lowercase Go error without exposing the raw
// result or tool error. Process records at most one post-execution audit event
// and does not mutate result.
func (p *ResultProcessor) Process(
	ctx context.Context,
	preflight Report,
	result any,
	executionErr error,
) (ProcessedResult, error) {
	started := time.Now()
	if p == nil || p.maxOutputBytes <= 0 || !validAuditFailureMode(p.auditFailureMode) {
		return minimalProcessedResult(), errors.New("tool safety result processor is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return minimalProcessedResult(), err
	}
	if !validateResultPreflight(preflight) {
		return minimalProcessedResult(), errors.New("tool safety result processor preflight report is invalid")
	}

	value, valueRedacted, err := processResultValue(result)
	if err != nil {
		return minimalProcessedResult(), errors.New("tool safety result is not JSON serializable")
	}
	executionError, errorRedacted := processExecutionError(executionErr)
	processed := ProcessedResult{
		Value:          value,
		ExecutionError: executionError,
		Redacted:       valueRedacted || errorRedacted,
	}
	processed, err = p.enforceOutputBudget(processed)
	if err != nil {
		return processed, err
	}
	if err := ctx.Err(); err != nil {
		return processed, err
	}

	event := resultAuditEvent(
		preflight, processed, executionErr != nil,
		time.Since(started).Milliseconds(),
	)
	if p.auditSink != nil {
		if err := p.auditSink.Record(ctx, event); err != nil &&
			p.auditFailureMode == AuditRequired {
			return processed, fmt.Errorf("record required tool safety result audit: %w", err)
		}
	}
	return processed, nil
}

func processResultValue(result any) (any, bool, error) {
	byteReplacements, err := resultByteReplacements(result)
	if err != nil {
		return nil, false, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, false, err
	}
	if err := validateResultByteReplacements(value, byteReplacements); err != nil {
		return nil, false, err
	}
	value, changed := redactJSONValueWithBytes(value, byteReplacements)
	return value, changed, nil
}

type resultByteReplacement struct {
	value       string
	occurrences int
}

// resultByteReplacements correlates encoding/json's base64 representation with
// byte slices that require redaction or omission without mutating caller data.
func resultByteReplacements(result any) (map[string]resultByteReplacement, error) {
	replacements := make(map[string]resultByteReplacement)
	if err := collectResultByteReplacements(
		reflect.ValueOf(result), 0, replacements,
	); err != nil {
		return nil, err
	}
	return replacements, nil
}

func collectResultByteReplacements(
	value reflect.Value,
	depth int,
	replacements map[string]resultByteReplacement,
) error {
	if !value.IsValid() {
		return nil
	}
	if depth > maxResultByteDepth {
		return errors.New("tool safety result exceeds byte scan depth")
	}
	if value.Type() == reflect.TypeOf(json.RawMessage{}) {
		return nil
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if !value.IsNil() {
			return collectResultByteReplacements(
				value.Elem(), depth+1, replacements,
			)
		}
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			collectResultByteSlice(value, replacements)
			return nil
		}
		for index := 0; index < value.Len(); index++ {
			if err := collectResultByteReplacements(
				value.Index(index), depth+1, replacements,
			); err != nil {
				return err
			}
		}
	case reflect.Array:
		for index := 0; index < value.Len(); index++ {
			if err := collectResultByteReplacements(
				value.Index(index), depth+1, replacements,
			); err != nil {
				return err
			}
		}
	case reflect.Map:
		iterator := value.MapRange()
		for iterator.Next() {
			if err := collectResultByteReplacements(
				iterator.Value(), depth+1, replacements,
			); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			if field.PkgPath != "" ||
				strings.SplitN(field.Tag.Get("json"), ",", 2)[0] == "-" {
				continue
			}
			if err := collectResultByteReplacements(
				value.Field(index), depth+1, replacements,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func collectResultByteSlice(
	value reflect.Value,
	replacements map[string]resultByteReplacement,
) {
	if value.IsNil() || value.Len() == 0 {
		return
	}
	raw := make([]byte, value.Len())
	for index := range raw {
		raw[index] = byte(value.Index(index).Uint())
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	replacement := resultByteReplacement{occurrences: 1}
	if !textualResultBytes(raw) {
		replacement.value = binaryResultOmitted
	} else {
		changed := false
		redacted := redactReportString(string(raw), &changed)
		if !changed {
			return
		}
		replacement.value = redacted
	}
	if existing, ok := replacements[encoded]; ok {
		replacement.occurrences += existing.occurrences
	}
	replacements[encoded] = replacement
}

func textualResultBytes(value []byte) bool {
	if !utf8.Valid(value) {
		return false
	}
	for _, character := range string(value) {
		if unicode.IsControl(character) && character != '\n' &&
			character != '\r' && character != '\t' {
			return false
		}
	}
	return true
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func validateResultByteReplacements(
	value any,
	replacements map[string]resultByteReplacement,
) error {
	counts := make(map[string]int, len(replacements))
	if err := countResultStrings(value, 0, replacements, counts); err != nil {
		return err
	}
	for encoded, replacement := range replacements {
		if counts[encoded] != replacement.occurrences {
			return errors.New("tool safety result has ambiguous byte encoding")
		}
	}
	return nil
}

func countResultStrings(
	value any,
	depth int,
	replacements map[string]resultByteReplacement,
	counts map[string]int,
) error {
	if depth > maxResultByteDepth {
		return errors.New("tool safety result exceeds JSON scan depth")
	}
	switch current := value.(type) {
	case map[string]any:
		for _, child := range current {
			if err := countResultStrings(
				child, depth+1, replacements, counts,
			); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range current {
			if err := countResultStrings(
				child, depth+1, replacements, counts,
			); err != nil {
				return err
			}
		}
	case string:
		if _, ok := replacements[current]; ok {
			counts[current]++
		}
	}
	return nil
}

func redactJSONValueWithBytes(
	value any,
	byteReplacements map[string]resultByteReplacement,
) (any, bool) {
	switch current := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(current))
		changed := false
		for key, child := range current {
			redactedKey := key
			if replacement := redactReportString(key, &changed); replacement != key {
				redactedKey = replacement
			} else if replacement, keyChanged := redactRecoverableBase64(key); keyChanged {
				redactedKey, _ = replacement.(string)
				changed = true
			}
			if isSensitiveResultName(key) {
				if text, ok := child.(string); !ok || text != internalredact.Value {
					changed = true
				}
				redacted[redactedKey] = internalredact.Value
				continue
			}
			redactedChild, childChanged := redactJSONValueWithBytes(
				child, byteReplacements,
			)
			changed = changed || childChanged
			redacted[redactedKey] = redactedChild
		}
		return redacted, changed
	case []any:
		if raw, ok := resultByteArray(current); ok {
			changed := false
			redacted := redactReportString(string(raw), &changed)
			if changed {
				return redacted, true
			}
		}
		redacted := make([]any, len(current))
		changed := false
		for index, child := range current {
			redacted[index], changed = redactJSONChildWithBytes(
				child, changed, byteReplacements,
			)
		}
		return redacted, changed
	case string:
		if replacement, ok := byteReplacements[current]; ok {
			return replacement.value, true
		}
		changed := false
		redacted := redactReportString(current, &changed)
		if changed {
			return redacted, true
		}
		return redactRecoverableBase64(current)
	default:
		return value, false
	}
}

func resultByteArray(value []any) ([]byte, bool) {
	if len(value) == 0 {
		return nil, false
	}
	result := make([]byte, len(value))
	for index, item := range value {
		number, ok := item.(json.Number)
		if !ok {
			return nil, false
		}
		parsed, err := number.Int64()
		if err != nil || parsed < 0 || parsed > 255 {
			return nil, false
		}
		result[index] = byte(parsed)
	}
	return result, textualResultBytes(result)
}

func redactRecoverableBase64(value string) (any, bool) {
	if len(value) < 8 {
		return value, false
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return value, false
	}
	changed := false
	redactReportString(string(decoded), &changed)
	if changed && !textualResultBytes(decoded) {
		return binaryResultOmitted, true
	}
	if !changed || !textualResultBytes(decoded) {
		return value, false
	}
	redacted := redactReportString(string(decoded), &changed)
	return redacted, true
}

func redactJSONChildWithBytes(
	child any,
	alreadyChanged bool,
	byteReplacements map[string]resultByteReplacement,
) (any, bool) {
	redacted, changed := redactJSONValueWithBytes(child, byteReplacements)
	return redacted, alreadyChanged || changed
}

func isSensitiveResultName(name string) bool {
	words := normalizedResultNameWords(name)
	if internalredact.IsSensitiveName(name) {
		return true
	}
	for index, word := range words {
		if internalredact.IsSensitiveName(word) {
			return true
		}
		if isSensitiveCompactCredential(word) {
			return true
		}
		switch word {
		case "token", "secret", "password", "passwd", "credential",
			"credentials", "authorization", "apikey", "accesskey",
			"privatekey", "clientsecret":
			return true
		}
		if index+1 >= len(words) || words[index+1] != "key" {
			continue
		}
		switch word {
		case "api", "access", "private":
			return true
		}
	}
	return false
}

func isSensitiveCompactCredential(word string) bool {
	if strings.Contains(word, "credentials") {
		return true
	}
	if !strings.Contains(word, "credential") {
		return false
	}
	switch word {
	case "credentialed", "credentialing", "uncredentialed",
		"uncredentialing", "noncredentialed", "noncredentialing":
		return false
	default:
		return true
	}
}

func normalizedResultNameWords(name string) []string {
	runes := []rune(name)
	words := make([]string, 0, 4)
	var word strings.Builder
	flush := func() {
		if word.Len() == 0 {
			return
		}
		words = append(words, strings.ToLower(word.String()))
		word.Reset()
	}
	for index, char := range runes {
		if !unicode.IsLetter(char) && !unicode.IsDigit(char) {
			flush()
			continue
		}
		if index > 0 && unicode.IsUpper(char) && word.Len() > 0 {
			previous := runes[index-1]
			nextIsLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if unicode.IsLower(previous) || unicode.IsDigit(previous) ||
				(unicode.IsUpper(previous) && nextIsLower) {
				flush()
			}
		}
		word.WriteRune(char)
	}
	flush()
	return words
}

func processExecutionError(executionErr error) (string, bool) {
	if executionErr == nil {
		return "", false
	}
	raw := executionErr.Error()
	changed := false
	redacted := redactReportString(raw, &changed)
	singleLine := strings.Join(strings.Fields(redacted), " ")
	return singleLine, changed || singleLine != raw
}

func (p *ResultProcessor) enforceOutputBudget(
	processed ProcessedResult,
) (ProcessedResult, error) {
	if processedResultFits(processed, p.maxOutputBytes) {
		return processed, nil
	}
	processed.Value = safeOmittedValue()
	processed.Truncated = true
	if processedResultFits(processed, p.maxOutputBytes) {
		return processed, nil
	}
	processed.ExecutionError = ""
	if processedResultFits(processed, p.maxOutputBytes) {
		return processed, nil
	}
	return minimalProcessedResult(), errors.New("tool safety result processor output limit is too small")
}

func processedResultFits(processed ProcessedResult, limit int64) bool {
	encoded, err := json.Marshal(processed)
	return err == nil && int64(len(encoded)) <= limit
}

func safeOmittedValue() map[string]any {
	return map[string]any{
		"status": "omitted",
		"reason": "tool output exceeded the configured safety limit",
	}
}

func minimalProcessedResult() ProcessedResult {
	return ProcessedResult{Truncated: true}
}

func validateResultPreflight(report Report) bool {
	return report.SchemaVersion > 0 && strings.TrimSpace(report.ScanID) != "" &&
		validDecision(report.Decision) && validRiskLevel(report.RiskLevel) &&
		validBackend(report.Backend) && strings.TrimSpace(report.RuleID) != ""
}

func validRiskLevel(risk RiskLevel) bool {
	return risk == RiskLow || risk == RiskMedium || risk == RiskHigh ||
		risk == RiskCritical
}

func validBackend(backend Backend) bool {
	// Guard reports preserve an omitted Request backend as the Backend zero
	// value, so accepting it keeps every Guard-produced report processable.
	return backend == "" || backend == BackendWorkspaceExec || backend == BackendHostExec ||
		backend == BackendCodeExec || backend == BackendUnknown
}

func resultAuditEvent(
	preflight Report,
	processed ProcessedResult,
	executionFailed bool,
	durationMillis int64,
) AuditEvent {
	return AuditEvent{
		SchemaVersion:   preflight.SchemaVersion,
		Timestamp:       time.Now().UTC(),
		ScanID:          preflight.ScanID,
		Stage:           resultAuditStage,
		ToolName:        preflight.ToolName,
		Backend:         preflight.Backend,
		Decision:        preflight.Decision,
		RiskLevel:       preflight.RiskLevel,
		RuleID:          preflight.RuleID,
		DurationMillis:  durationMillis,
		Redacted:        processed.Redacted,
		Intercepted:     false,
		ExecutionStatus: resultExecutionStatus(processed, executionFailed),
	}
}

func resultExecutionStatus(
	processed ProcessedResult,
	executionFailed bool,
) string {
	switch {
	case processed.Truncated:
		return "truncated"
	case executionFailed:
		return "execution_error"
	case processed.Redacted:
		return "redacted"
	default:
		return "success"
	}
}
