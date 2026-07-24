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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	internalredact "trpc.group/trpc-go/trpc-agent-go/internal/redact"
)

const (
	resultAuditStage = "post_execute"

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
// AuditBestEffort. A nil sink disables post-execution audit writes, including
// in AuditRequired mode. Nil options are ignored. The caller retains ownership
// of guard and sink.
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
	return processor, nil
}

// WithResultAuditFailureMode configures post-execution audit handling.
// AuditBestEffort returns a safe processed value when the sink fails;
// AuditRequired returns that value together with an error preserving the sink
// cause. Unsupported modes cause NewResultProcessor to return an error.
func WithResultAuditFailureMode(mode AuditFailureMode) ResultOption {
	return func(processor *ResultProcessor) {
		if processor != nil {
			processor.auditFailureMode = mode
		}
	}
}

// Process copies result through JSON, recursively redacts sensitive data,
// converts executionErr to redacted single-line data, and enforces the guard's
// byte limit over the complete serialized ProcessedResult. Callers invoke it
// explicitly after execution and their normal callbacks. A nil context is
// treated as context.Background. Invalid preflight data, cancellation,
// unsupported or cyclic result values, a nil or zero receiver, an output limit
// too small for the safe omission marker, or a required audit failure returns
// a lowercase Go error without exposing the raw result or tool error. Process
// records at most one post-execution audit event and does not mutate result.
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

	event := resultAuditEvent(preflight, processed, time.Since(started).Milliseconds())
	if p.auditSink != nil {
		if err := p.auditSink.Record(ctx, event); err != nil &&
			p.auditFailureMode == AuditRequired {
			return processed, fmt.Errorf("record required tool safety result audit: %w", err)
		}
	}
	return processed, nil
}

func processResultValue(result any) (any, bool, error) {
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
	value, changed := redactJSONValue(value)
	return value, changed, nil
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

func redactJSONValue(value any) (any, bool) {
	switch current := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(current))
		changed := false
		for key, child := range current {
			redactedKey := key
			if replacement := redactReportString(key, &changed); replacement != key {
				redactedKey = replacement
			}
			if isSensitiveResultName(key) {
				if text, ok := child.(string); !ok || text != internalredact.Value {
					changed = true
				}
				redacted[redactedKey] = internalredact.Value
				continue
			}
			redactedChild, childChanged := redactJSONValue(child)
			changed = changed || childChanged
			redacted[redactedKey] = redactedChild
		}
		return redacted, changed
	case []any:
		redacted := make([]any, len(current))
		changed := false
		for index, child := range current {
			redacted[index], changed = redactJSONChild(child, changed)
		}
		return redacted, changed
	case string:
		changed := false
		return redactReportString(current, &changed), changed
	default:
		return value, false
	}
}

func redactJSONChild(child any, alreadyChanged bool) (any, bool) {
	redacted, changed := redactJSONValue(child)
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
		ExecutionStatus: resultExecutionStatus(processed),
	}
}

func resultExecutionStatus(processed ProcessedResult) string {
	switch {
	case processed.Truncated:
		return "truncated"
	case processed.Redacted:
		return "redacted"
	case processed.ExecutionError != "":
		return "execution_error"
	default:
		return "success"
	}
}
