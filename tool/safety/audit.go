//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	auditSchemaVersion       = "1.0"
	maxAuditPreviewRunes     = 256
	maxReportPayloadBytes    = 64 << 10
	maxSafetyIdentifierRunes = 128
	maxSafetyTextRunes       = 1024

	omittedReportPayload    = "[PAYLOAD OMITTED: exceeds report byte limit]"
	omittedSafetyIdentifier = "[OMITTED: identifier exceeds safety limit]"
	omittedSafetyText       = "[OMITTED: text exceeds safety limit]"
	omittedAuditPreview     = "[OMITTED: preview exceeds safety limit]"
)

// AuditEvent is the bounded, redacted record emitted for one safety decision.
// It intentionally has no raw argument, environment, script, or output field.
// RequestSHA256 is correlatable and should be protected like other audit data.
type AuditEvent struct {
	SchemaVersion  string                `json:"schema_version"`
	PolicyID       string                `json:"policy_id"`
	Timestamp      time.Time             `json:"timestamp"`
	ToolName       string                `json:"tool_name"`
	ToolCallID     string                `json:"tool_call_id,omitempty"`
	Backend        Backend               `json:"backend"`
	Decision       tool.PermissionAction `json:"decision"`
	RiskLevel      RiskLevel             `json:"risk_level"`
	RuleID         string                `json:"rule_id"`
	RuleIDs        []string              `json:"rule_ids,omitempty"`
	Evidence       string                `json:"evidence,omitempty"`
	Recommendation string                `json:"recommendation,omitempty"`
	Blocked        bool                  `json:"blocked"`
	Redacted       bool                  `json:"redacted"`
	RedactionCount int                   `json:"redaction_count,omitempty"`
	DurationMS     int64                 `json:"duration_ms"`
	RequestSHA256  string                `json:"request_sha256,omitempty"`
	CommandPreview string                `json:"command_preview,omitempty"`
}

// AuditSink persists safety decisions. Implementations should be concurrency-safe.
type AuditSink interface {
	WriteAudit(context.Context, AuditEvent) error
}

// AuditSinkFunc adapts a function into an AuditSink.
type AuditSinkFunc func(context.Context, AuditEvent) error

// WriteAudit implements AuditSink and returns an error for a nil function.
func (f AuditSinkFunc) WriteAudit(ctx context.Context, event AuditEvent) error {
	if f == nil {
		return errors.New("tool safety: audit sink function is nil")
	}
	return f(ctx, event)
}

// JSONLAuditSink writes one independently encoded JSON object per line.
type JSONLAuditSink struct {
	mu       sync.Mutex
	writer   io.Writer
	redactor Redactor
}

// NewJSONLAuditSink constructs a concurrency-safe JSON Lines audit sink.
// The writer is owned by the caller and is never closed by the sink.
func NewJSONLAuditSink(writer io.Writer) *JSONLAuditSink {
	return &JSONLAuditSink{
		writer:   writer,
		redactor: NewRedactor(),
	}
}

// WriteAudit redacts all human-readable fields before writing one complete line.
func (s *JSONLAuditSink) WriteAudit(_ context.Context, event AuditEvent) error {
	if s == nil || s.writer == nil {
		return errors.New("tool safety: audit writer is nil")
	}
	event = sanitizeAuditEvent(s.redactor, event)
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	return writeAll(s.writer, encoded)
}

func writeAll(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(value) {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}

func normalizeRequestDigest(value string) (string, bool) {
	const prefix = "sha256:"
	value = strings.TrimSpace(value)
	if len(value) != len(prefix)+sha256.Size*2 ||
		!strings.EqualFold(value[:len(prefix)], prefix) {
		return "", false
	}
	hexValue := strings.ToLower(value[len(prefix):])
	if _, err := hex.DecodeString(hexValue); err != nil {
		return "", false
	}
	return prefix + hexValue, true
}

func sanitizeReport(redactor Redactor, report Report) Report {
	if isNilRedactor(redactor) {
		redactor = NewRedactor()
	}
	report.Matches = append([]Match(nil), report.Matches...)
	count := report.redactionCount
	changed := report.Redacted || report.Command == omittedReportPayload
	report.ToolName = sanitizeBoundedRunes(
		redactor,
		report.ToolName,
		maxSafetyIdentifierRunes,
		omittedSafetyIdentifier,
		&count,
		&changed,
	)
	report.Backend = normalizeBackend(report.Backend)
	report.RuleID = sanitizeBoundedRunes(
		redactor,
		report.RuleID,
		maxSafetyIdentifierRunes,
		omittedSafetyIdentifier,
		&count,
		&changed,
	)
	report.Command = sanitizeBoundedBytes(
		redactor,
		report.Command,
		maxReportPayloadBytes,
		omittedReportPayload,
		&count,
		&changed,
	)
	report.Evidence = sanitizeBoundedRunes(
		redactor,
		report.Evidence,
		maxSafetyTextRunes,
		omittedSafetyText,
		&count,
		&changed,
	)
	report.Recommendation = sanitizeBoundedRunes(
		redactor,
		report.Recommendation,
		maxSafetyTextRunes,
		omittedSafetyText,
		&count,
		&changed,
	)
	for index := range report.Matches {
		report.Matches[index].RuleID = sanitizeBoundedRunes(
			redactor,
			report.Matches[index].RuleID,
			maxSafetyIdentifierRunes,
			omittedSafetyIdentifier,
			&count,
			&changed,
		)
		report.Matches[index].Evidence = sanitizeBoundedRunes(
			redactor,
			report.Matches[index].Evidence,
			maxSafetyTextRunes,
			omittedSafetyText,
			&count,
			&changed,
		)
		report.Matches[index].Recommendation = sanitizeBoundedRunes(
			redactor,
			report.Matches[index].Recommendation,
			maxSafetyTextRunes,
			omittedSafetyText,
			&count,
			&changed,
		)
	}
	report.Redacted = changed || count > 0
	report.redactionCount = count
	return report
}

func sanitizeAuditEvent(redactor Redactor, event AuditEvent) AuditEvent {
	if isNilRedactor(redactor) {
		redactor = NewRedactor()
	}
	count := event.RedactionCount
	changed := event.Redacted
	if count < 0 {
		count = 0
		changed = true
	}
	if event.SchemaVersion != auditSchemaVersion {
		event.SchemaVersion = auditSchemaVersion
		changed = true
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}
	event.Backend = normalizeBackend(event.Backend)
	if validateAction(event.Decision) != nil {
		event.Decision = tool.PermissionActionAsk
		changed = true
	}
	if riskRank(event.RiskLevel) == 0 {
		event.RiskLevel = RiskLevelHigh
		changed = true
	}
	event.Blocked = event.Decision != tool.PermissionActionAllow
	if event.DurationMS < 0 {
		event.DurationMS = 0
		changed = true
	}
	if digest, ok := normalizeRequestDigest(event.RequestSHA256); ok {
		event.RequestSHA256 = digest
	} else if event.RequestSHA256 != "" {
		event.RequestSHA256 = ""
		changed = true
	}
	if len(event.RuleIDs) > maxReportMatches {
		event.RuleIDs = append(
			[]string(nil), event.RuleIDs[:maxReportMatches-1]...,
		)
		event.RuleIDs = append(event.RuleIDs, "limits.rule_ids")
		changed = true
	} else {
		event.RuleIDs = append([]string(nil), event.RuleIDs...)
	}
	event.ToolName = sanitizeBoundedRunes(
		redactor,
		event.ToolName,
		maxSafetyIdentifierRunes,
		omittedSafetyIdentifier,
		&count,
		&changed,
	)
	event.ToolCallID = sanitizeBoundedRunes(
		redactor,
		event.ToolCallID,
		maxSafetyIdentifierRunes,
		omittedSafetyIdentifier,
		&count,
		&changed,
	)
	event.PolicyID = sanitizeBoundedRunes(
		redactor,
		event.PolicyID,
		maxSafetyIdentifierRunes,
		omittedSafetyIdentifier,
		&count,
		&changed,
	)
	event.RuleID = sanitizeBoundedRunes(
		redactor,
		event.RuleID,
		maxSafetyIdentifierRunes,
		omittedSafetyIdentifier,
		&count,
		&changed,
	)
	for index := range event.RuleIDs {
		event.RuleIDs[index] = sanitizeBoundedRunes(
			redactor,
			event.RuleIDs[index],
			maxSafetyIdentifierRunes,
			omittedSafetyIdentifier,
			&count,
			&changed,
		)
	}
	event.Evidence = sanitizeBoundedRunes(
		redactor,
		event.Evidence,
		maxSafetyTextRunes,
		omittedSafetyText,
		&count,
		&changed,
	)
	event.Recommendation = sanitizeBoundedRunes(
		redactor,
		event.Recommendation,
		maxSafetyTextRunes,
		omittedSafetyText,
		&count,
		&changed,
	)
	event.CommandPreview = sanitizeBoundedRunes(
		redactor,
		event.CommandPreview,
		maxAuditPreviewRunes,
		omittedAuditPreview,
		&count,
		&changed,
	)
	event.RedactionCount = count
	event.Redacted = changed || count > 0
	return event
}

func sanitizeBoundedRunes(
	redactor Redactor,
	value string,
	limit int,
	omission string,
	count *int,
	changed *bool,
) string {
	bounded, omitted := omitOverlongRunes(value, limit, omission)
	if omitted {
		*changed = true
		return bounded
	}
	redacted, found := redactor.RedactString(bounded)
	*count += found
	if found > 0 {
		*changed = true
	}
	bounded, omitted = omitOverlongRunes(redacted, limit, omission)
	if omitted {
		*changed = true
		return bounded
	}
	return bounded
}

func sanitizeBoundedBytes(
	redactor Redactor,
	value string,
	limit int,
	omission string,
	count *int,
	changed *bool,
) string {
	if limit > 0 && len(value) > limit {
		*changed = true
		return omission
	}
	redacted, found := redactor.RedactString(value)
	*count += found
	if found > 0 {
		*changed = true
	}
	if limit > 0 && len(redacted) > limit {
		*changed = true
		return omission
	}
	return redacted
}

func omitOverlongRunes(value string, limit int, omission string) (string, bool) {
	if limit <= 0 {
		return value, false
	}
	count := 0
	for range value {
		if count == limit {
			return omission, true
		}
		count++
	}
	return value, false
}

func writeGuardAudit(
	ctx context.Context,
	sink AuditSink,
	redactor Redactor,
	request Request,
	report Report,
	policyID string,
) error {
	if sink == nil {
		return nil
	}
	if isNilAuditSink(sink) {
		return errors.New("tool safety: audit sink is nil")
	}
	if isNilRedactor(redactor) {
		redactor = NewRedactor()
	}
	rawCommand := requestPayload(request)
	cleanPreview, previewRedactions := redactor.RedactString(rawCommand)
	if report.Command != "" {
		cleanPreview = report.Command
	}
	redactionCount := report.redactionCount
	if report.Command == "" {
		redactionCount += previewRedactions
	}
	event := AuditEvent{
		SchemaVersion:  auditSchemaVersion,
		Timestamp:      time.Now().UTC(),
		PolicyID:       policyID,
		ToolName:       report.ToolName,
		ToolCallID:     request.ToolCallID,
		Backend:        report.Backend,
		Decision:       report.Decision,
		RiskLevel:      report.RiskLevel,
		RuleID:         report.RuleID,
		RuleIDs:        reportRuleIDs(report),
		Evidence:       report.Evidence,
		Recommendation: report.Recommendation,
		Blocked:        report.Blocked,
		Redacted:       report.Redacted || redactionCount > 0,
		RedactionCount: redactionCount,
		DurationMS:     report.DurationMS,
		RequestSHA256:  hashRequestPayload(request),
		CommandPreview: truncateRunes(cleanPreview, maxAuditPreviewRunes),
	}
	event = sanitizeAuditEvent(redactor, event)
	return writeAuditSafely(ctx, sink, event)
}

func writeAuditSafely(
	ctx context.Context,
	sink AuditSink,
	event AuditEvent,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.New("tool safety: audit sink panicked")
		}
	}()
	return sink.WriteAudit(ctx, event)
}

func isNilAuditSink(sink AuditSink) bool {
	value := reflect.ValueOf(sink)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func reportRuleIDs(report Report) []string {
	seen := make(map[string]struct{}, len(report.Matches)+1)
	ruleIDs := make([]string, 0, len(report.Matches)+1)
	appendRule := func(ruleID string) {
		if ruleID == "" {
			return
		}
		if _, ok := seen[ruleID]; ok {
			return
		}
		seen[ruleID] = struct{}{}
		ruleIDs = append(ruleIDs, ruleID)
	}
	appendRule(report.RuleID)
	for _, match := range report.Matches {
		appendRule(match.RuleID)
	}
	return ruleIDs
}

func requestHasPayload(request Request) bool {
	if request.Command != "" || request.Script != "" || request.SessionInput != "" {
		return true
	}
	for _, block := range request.CodeBlocks {
		if block.Code != "" {
			return true
		}
	}
	return false
}

func requestPayload(request Request) string {
	parts := make([]string, 0, 2)
	size := 0
	appendPart := func(part string) bool {
		if part == "" {
			return true
		}
		separatorBytes := 0
		if len(parts) > 0 {
			separatorBytes = 1
		}
		if len(part) > maxReportPayloadBytes-size-separatorBytes {
			return false
		}
		size += separatorBytes + len(part)
		parts = append(parts, part)
		return true
	}
	if !appendPart(request.Command) || !appendPart(request.Script) {
		return omittedReportPayload
	}
	for _, block := range request.CodeBlocks {
		if !appendPart(block.Code) {
			return omittedReportPayload
		}
	}
	return strings.Join(parts, "\n")
}

func hashRequestPayload(request Request) string {
	digest := sha256.New()
	wrote := false
	writeValue := func(label string, value string) {
		wrote = true
		writeHashString(digest, label)
		_, _ = digest.Write([]byte{0})
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = digest.Write(length[:])
		writeHashString(digest, value)
	}

	writeValue("tool.name", normalizedToolName(request.ToolName))
	writeValue("backend", string(normalizeBackend(request.Backend)))
	hashCommandAndCode(request, writeValue)
	hashExecutionContext(request, writeValue)
	hashStructuredExecution(request, writeValue)
	if !wrote {
		return ""
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil))
}

func hashCommandAndCode(request Request, writeValue func(string, string)) {
	writeNonEmptyHashValue(writeValue, "skill", request.Skill)
	writeNonEmptyHashValue(writeValue, "execution.id", request.ExecutionID)
	writeNonEmptyHashValue(writeValue, "command", request.Command)
	if request.Script != "" {
		writeValue("script.language", request.Language)
		writeValue("script.content", request.Script)
	}
	if len(request.CodeBlocks) == 0 {
		return
	}
	writeValue("code.count", strconv.Itoa(len(request.CodeBlocks)))
	for index, block := range request.CodeBlocks {
		prefix := "code." + strconv.Itoa(index) + "."
		writeValue(prefix+"language", block.Language)
		writeValue(prefix+"content", block.Code)
	}
}

func hashExecutionContext(request Request, writeValue func(string, string)) {
	writeNonEmptyHashValue(writeValue, "cwd", request.CWD)
	hashEnvironment(request.Env, writeValue)
	if timeout := request.EffectiveTimeout(); timeout != 0 {
		writeValue("timeout_ns", strconv.FormatInt(int64(timeout), 10))
	}
	if request.MaxOutputBytes != 0 {
		writeValue(
			"max_output_bytes",
			strconv.FormatInt(request.MaxOutputBytes, 10),
		)
	}
	if request.Background {
		writeValue("background", "true")
	}
	if request.TTY {
		writeValue("tty", "true")
	}
	writeNonEmptyHashValue(writeValue, "session.id", request.SessionID)
	writeNonEmptyHashValue(writeValue, "session.input", request.SessionInput)
	if request.YieldMS != nil {
		writeValue("yield_ms", strconv.Itoa(*request.YieldMS))
	}
	if request.PollLines != 0 {
		writeValue("poll_lines", strconv.Itoa(request.PollLines))
	}
	writeNonEmptyHashValue(writeValue, "editor.text", request.EditorText)
}

func hashEnvironment(
	environment map[string]string,
	writeValue func(string, string),
) {
	if len(environment) == 0 {
		return
	}
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	writeValue("env.count", strconv.Itoa(len(keys)))
	for index, key := range keys {
		prefix := "env." + strconv.Itoa(index) + "."
		writeValue(prefix+"key", key)
		writeValue(prefix+"value", environment[key])
	}
}

func hashStructuredExecution(
	request Request,
	writeValue func(string, string),
) {
	hashInputSpecs(request.Inputs, writeValue)
	hashOutputSpecs(request.OutputFiles, request.Outputs, writeValue)
	if request.SaveArtifacts {
		writeValue("artifacts.save", "true")
	}
	if request.OmitInline {
		writeValue("artifacts.omit_inline", "true")
	}
	writeNonEmptyHashValue(
		writeValue,
		"artifacts.prefix",
		request.ArtifactPrefix,
	)
}

func hashInputSpecs(inputs []InputSpec, writeValue func(string, string)) {
	if len(inputs) == 0 {
		return
	}
	writeValue("input.count", strconv.Itoa(len(inputs)))
	for index, input := range inputs {
		prefix := "input." + strconv.Itoa(index) + "."
		writeValue(prefix+"from", input.From)
		writeValue(prefix+"to", input.To)
		writeValue(prefix+"mode", input.Mode)
		writeValue(prefix+"pin", strconv.FormatBool(input.Pin))
	}
}

func hashOutputSpecs(
	outputFiles []string,
	outputs *OutputSpec,
	writeValue func(string, string),
) {
	if len(outputFiles) > 0 {
		writeValue("output_file.count", strconv.Itoa(len(outputFiles)))
		for index, outputFile := range outputFiles {
			writeValue(
				"output_file."+strconv.Itoa(index),
				outputFile,
			)
		}
	}
	if outputs == nil {
		return
	}
	writeValue("outputs.present", "true")
	writeValue("outputs.glob.count", strconv.Itoa(len(outputs.Globs)))
	for index, glob := range outputs.Globs {
		writeValue("outputs.glob."+strconv.Itoa(index), glob)
	}
	writeValue("outputs.max_files", strconv.Itoa(outputs.MaxFiles))
	writeValue(
		"outputs.max_file_bytes",
		strconv.FormatInt(outputs.MaxFileBytes, 10),
	)
	writeValue(
		"outputs.max_total_bytes",
		strconv.FormatInt(outputs.MaxTotalBytes, 10),
	)
	writeValue("outputs.save", strconv.FormatBool(outputs.Save))
	writeValue("outputs.name_template", outputs.NameTemplate)
	writeValue("outputs.inline", strconv.FormatBool(outputs.Inline))
}

func writeNonEmptyHashValue(
	writeValue func(string, string),
	label string,
	value string,
) {
	if value != "" {
		writeValue(label, value)
	}
}

func writeHashString(writer io.Writer, value string) {
	const chunkBytes = 32 << 10
	for len(value) > 0 {
		size := len(value)
		if size > chunkBytes {
			size = chunkBytes
		}
		_, _ = writer.Write([]byte(value[:size]))
		value = value[size:]
	}
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return value
	}
	count := 0
	for index := range value {
		if count == limit {
			return value[:index]
		}
		count++
	}
	return value
}

// WithAuditSink records each Guard decision. An audit persistence failure may
// make the final decision more restrictive according to ActionPolicy.
func WithAuditSink(sink AuditSink) Option {
	return func(guard *Guard) {
		if guard != nil {
			guard.auditSink = sink
		}
	}
}

// WithRedactor adds organization-specific redaction after the built-in
// credential redactor. The supplied implementation must be concurrency-safe,
// non-mutating, and idempotent.
func WithRedactor(redactor Redactor) Option {
	return func(guard *Guard) {
		if guard != nil && !isNilRedactor(redactor) {
			guard.redactor = chainRedactors(guard.redactor, redactor)
		}
	}
}

// WithAuditErrorHook observes audit persistence failures. Guard decisions are
// not changed by hook failures or by a missing hook. The hook must be safe for
// concurrent use.
func WithAuditErrorHook(hook func(error)) Option {
	return func(guard *Guard) {
		if guard != nil {
			guard.auditError = hook
		}
	}
}
