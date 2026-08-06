//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type guardedTool struct {
	next    tool.CallableTool
	guard   *Guard
	backend Backend
}

// WrapTool protects direct CallableTool calls that do not pass through the
// agent runner's PermissionPolicy. Deny and ask decisions return the same
// structured result used by the runner without calling the wrapped tool.
func WrapTool(
	next tool.CallableTool,
	guard *Guard,
	backend Backend,
) tool.CallableTool {
	if guard == nil {
		guard = NewGuard(nil)
	}
	return &guardedTool{
		next:    next,
		guard:   guard,
		backend: backend,
	}
}

func (w *guardedTool) Declaration() *tool.Declaration {
	if w == nil || w.next == nil {
		return nil
	}
	return w.next.Declaration()
}

func (w *guardedTool) ToolMetadata() tool.ToolMetadata {
	if w == nil {
		return tool.ToolMetadata{}
	}
	return tool.MetadataOf(w.next)
}

func (w *guardedTool) Call(
	ctx context.Context,
	arguments []byte,
) (any, error) {
	if w == nil || w.next == nil {
		return nil, errors.New("tool safety wrapper has no underlying tool")
	}
	start := time.Now()
	report := w.scan(ctx, arguments)
	if report.Blocked {
		report.DurationMicros = time.Since(start).Microseconds()
		w.guard.emitBestEffort(ctx, report)
		decision := permissionDecision(report)
		name := report.ToolName
		return tool.PermissionResultFor(name, decision), nil
	}

	runCtx, cancel := w.guard.executionContext(ctx)
	defer cancel()
	result, callErr := w.next.Call(runCtx, arguments)
	state := newSanitizeState(
		w.guard.scanner.policy.Limits.MaxOutputBytes,
	)
	result = sanitizeResult(result, state)
	callErr = state.err(callErr)
	report = outputReport(report, state.redacted, state.truncated)
	report.DurationMicros = time.Since(start).Microseconds()
	w.guard.emitBestEffort(ctx, report)
	return result, callErr
}

func (w *guardedTool) scan(ctx context.Context, arguments []byte) Report {
	declaration := w.next.Declaration()
	name := ""
	if declaration != nil {
		name = declaration.Name
	}
	request := &tool.PermissionRequest{
		Tool:        w.next,
		ToolName:    name,
		Declaration: declaration,
		Arguments:   arguments,
		Metadata:    tool.MetadataOf(w.next),
	}
	inputs, err := permissionInputs(request)
	if err != nil {
		return invalidRequestReport(
			name,
			w.backend,
			fmt.Sprintf("tool arguments are not valid JSON: %v", err),
		)
	}
	if len(inputs) == 0 {
		report := metadataReport(request)
		report.Backend = w.backend
		return report
	}
	reports := make([]Report, 0, len(inputs))
	for _, input := range inputs {
		input.Backend = w.backend
		reports = append(reports, w.guard.scanner.Scan(ctx, input))
	}
	return mergeReports(reports)
}

type guardedCodeExecutor struct {
	next    codeexecutor.CodeExecutor
	guard   *Guard
	backend Backend
}

// WrapCodeExecutor scans code blocks before invoking any local, container, or
// remote CodeExecutor and redacts or truncates returned output and artifacts.
func WrapCodeExecutor(
	next codeexecutor.CodeExecutor,
	guard *Guard,
	backend Backend,
) codeexecutor.CodeExecutor {
	if guard == nil {
		guard = NewGuard(nil)
	}
	return &guardedCodeExecutor{
		next:    next,
		guard:   guard,
		backend: backend,
	}
}

func (w *guardedCodeExecutor) ExecuteCode(
	ctx context.Context,
	input codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	if w == nil || w.next == nil {
		return codeexecutor.CodeExecutionResult{},
			errors.New("tool safety wrapper has no underlying code executor")
	}
	start := time.Now()
	reports := make([]Report, 0, len(input.CodeBlocks))
	for _, block := range input.CodeBlocks {
		reports = append(reports, w.guard.scanner.Scan(ctx, Input{
			ToolName: "execute_code",
			Script:   block.Code,
			Language: block.Language,
			Backend:  w.backend,
		}))
	}
	if len(reports) == 0 {
		reports = append(reports, invalidRequestReport(
			"execute_code",
			w.backend,
			"code execution input contains no code blocks",
		))
	}
	report := mergeReports(reports)
	if report.Blocked {
		report.DurationMicros = time.Since(start).Microseconds()
		w.guard.emitBestEffort(ctx, report)
		return codeexecutor.CodeExecutionResult{}, &BlockedError{Report: report}
	}

	runCtx, cancel := w.guard.executionContext(ctx)
	defer cancel()
	result, executeErr := w.next.ExecuteCode(runCtx, input)
	state := newSanitizeState(w.guard.scanner.policy.Limits.MaxOutputBytes)
	result.Output = state.string(result.Output)
	for i := range result.OutputFiles {
		result.OutputFiles[i].Name = state.string(result.OutputFiles[i].Name)
		result.OutputFiles[i].Content = state.string(
			result.OutputFiles[i].Content,
		)
		result.OutputFiles[i].Truncated = result.OutputFiles[i].Truncated ||
			state.truncated
	}
	executeErr = state.err(executeErr)
	report = outputReport(report, state.redacted, state.truncated)
	report.DurationMicros = time.Since(start).Microseconds()
	w.guard.emitBestEffort(ctx, report)
	return result, executeErr
}

func (w *guardedCodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	if w == nil || w.next == nil {
		return codeexecutor.CodeBlockDelimiter{}
	}
	return w.next.CodeBlockDelimiter()
}

func (g *Guard) executionContext(
	ctx context.Context,
) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	limit := time.Duration(
		g.scanner.policy.Limits.MaxTimeoutSeconds,
	) * time.Second
	return context.WithTimeout(ctx, limit)
}

func outputReport(
	report Report,
	redacted bool,
	truncated bool,
) Report {
	report.Redacted = report.Redacted || redacted
	switch {
	case redacted:
		report.RiskLevel = RiskHigh
		report.RuleID = RuleSensitiveLiteral
		report.Evidence = "sensitive output was redacted after execution"
		report.Recommendation = "Remove secrets from tool output and retain result redaction."
	case truncated:
		report.RiskLevel = RiskMedium
		report.RuleID = RuleOutputLimit
		report.Evidence = "tool output exceeded the configured byte limit"
		report.Recommendation = "Reduce output volume and retain the configured output cap."
	}
	return report
}

type sanitizeState struct {
	remaining int
	redacted  bool
	truncated bool
}

func newSanitizeState(maxBytes int) *sanitizeState {
	return &sanitizeState{remaining: maxBytes}
}

func (s *sanitizeState) string(value string) string {
	value, redacted, _ := redactText(value, 0)
	s.redacted = s.redacted || redacted
	if s.remaining <= 0 {
		if value != "" {
			s.truncated = true
			return truncatedValue
		}
		return ""
	}
	if len(value) <= s.remaining {
		s.remaining -= len(value)
		return value
	}
	value = utf8Prefix(value, s.remaining) + truncatedValue
	s.remaining = 0
	s.truncated = true
	return value
}

func (s *sanitizeState) err(err error) error {
	if err == nil {
		return nil
	}
	original := err.Error()
	sanitized := s.string(original)
	if sanitized == original {
		return err
	}
	return errors.New(sanitized)
}

func sanitizeResult(value any, state *sanitizeState) any {
	if value == nil {
		return nil
	}
	sanitized := sanitizeReflect(reflect.ValueOf(value), state)
	return sanitized.Interface()
}

func sanitizeReflect(value reflect.Value, state *sanitizeState) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.String:
		out := reflect.New(value.Type()).Elem()
		out.SetString(state.string(value.String()))
		return out
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		item := sanitizeReflect(value.Elem(), state)
		out := reflect.New(value.Type()).Elem()
		out.Set(item)
		return out
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.New(value.Type().Elem())
		out.Elem().Set(sanitizeReflect(value.Elem(), state))
		return out
	case reflect.Map:
		return sanitizeMap(value, state)
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			out.Index(i).Set(sanitizeReflect(value.Index(i), state))
		}
		return out
	case reflect.Array:
		out := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			out.Index(i).Set(sanitizeReflect(value.Index(i), state))
		}
		return out
	case reflect.Struct:
		out := reflect.New(value.Type()).Elem()
		out.Set(value)
		for i := 0; i < value.NumField(); i++ {
			if out.Field(i).CanSet() && value.Field(i).CanInterface() {
				out.Field(i).Set(sanitizeReflect(value.Field(i), state))
			}
		}
		return out
	default:
		return value
	}
}

func sanitizeMap(value reflect.Value, state *sanitizeState) reflect.Value {
	if value.IsNil() {
		return reflect.Zero(value.Type())
	}
	out := reflect.MakeMapWithSize(value.Type(), value.Len())
	iter := value.MapRange()
	for iter.Next() {
		key := iter.Key()
		if key.Kind() == reflect.String ||
			(key.Kind() == reflect.Interface &&
				!key.IsNil() &&
				key.Elem().Kind() == reflect.String) {
			key = sanitizeMapKey(key, state)
		}
		out.SetMapIndex(
			key,
			sanitizeReflect(iter.Value(), state),
		)
	}
	return out
}

func sanitizeMapKey(value reflect.Value, state *sanitizeState) reflect.Value {
	switch value.Kind() {
	case reflect.String:
		sanitized, redacted, _ := redactText(value.String(), 0)
		state.redacted = state.redacted || redacted
		out := reflect.New(value.Type()).Elem()
		out.SetString(sanitized)
		return out
	case reflect.Interface:
		item := sanitizeMapKey(value.Elem(), state)
		out := reflect.New(value.Type()).Elem()
		out.Set(item)
		return out
	default:
		return value
	}
}
