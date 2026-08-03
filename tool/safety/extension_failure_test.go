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
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type panickingExtractor struct{}

func (*panickingExtractor) Extract(
	*tool.PermissionRequest,
) (Request, bool, error) {
	panic("extractor secret must not escape")
}

type panickingRule struct{}

func (*panickingRule) Evaluate(context.Context, Request, Policy) []Match {
	panic("rule secret must not escape")
}

type panickingRedactor struct{}

func (*panickingRedactor) RedactString(string) (string, int) {
	panic("redactor string secret must not escape")
}

func (*panickingRedactor) RedactBytes([]byte) ([]byte, int) {
	panic("redactor bytes secret must not escape")
}

func (*panickingRedactor) RedactValue(any) (any, int) {
	panic("redactor value secret must not escape")
}

func TestRuntimeExtensionPanicsFailClosed(t *testing.T) {
	guard, err := New(
		testPolicy(),
		WithExtractor("custom_exec", &panickingExtractor{}),
		WithRule(&panickingRule{}),
	)
	require.NoError(t, err)

	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "custom_exec",
		Arguments: []byte("{}"),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "input.invalid_arguments")
	require.NotContains(t, decision.Reason, "secret")

	report, err := guard.Scan(
		context.Background(),
		commandRequest(BackendWorkspace, "go test ./tool/safety"),
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, report.Decision)
	require.True(t, hasRule(report, "rule.failure"))
	require.NotContains(t, report.Evidence, "secret")
}

func TestPanickingCustomRedactorFailsClosed(t *testing.T) {
	guard, err := New(testPolicy(), WithRedactor(&panickingRedactor{}))
	require.NoError(t, err)

	report, err := guard.Scan(
		context.Background(),
		commandRequest(BackendWorkspace, "go test ./tool/safety"),
	)
	require.NoError(t, err)
	require.True(t, report.Redacted)
	require.Equal(t, RedactedValue, report.Command)

	redactor := chainRedactors(NewRedactor(), &panickingRedactor{})
	bytesValue, count := redactor.RedactBytes([]byte("ordinary output"))
	require.Positive(t, count)
	require.Equal(t, []byte(RedactedValue), bytesValue)
	anyValue, count := redactor.RedactValue(map[string]any{"value": "ordinary"})
	require.Positive(t, count)
	require.Equal(t, RedactedValue, anyValue)
}

func TestPanickingAuditErrorHookCannotChangeDecision(t *testing.T) {
	policy := testPolicy()
	policy.Actions.AuditFailure = tool.PermissionActionDeny
	guard, err := New(
		policy,
		WithAuditSink(AuditSinkFunc(func(context.Context, AuditEvent) error {
			return errors.New("audit unavailable")
		})),
		WithAuditErrorHook(func(error) {
			panic("hook must not escape")
		}),
	)
	require.NoError(t, err)

	report, err := guard.Scan(
		context.Background(),
		commandRequest(BackendWorkspace, "go test ./tool/safety"),
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, report.Decision)
	require.True(t, hasRule(report, "audit.failure"))
}

type declarationPanicCallable struct {
	declarationCalls int
	callCount        int
}

func (t *declarationPanicCallable) Declaration() *tool.Declaration {
	t.declarationCalls++
	if t.declarationCalls > 1 {
		panic("dynamic declaration secret")
	}
	return &tool.Declaration{Name: "workspace_exec"}
}

func (t *declarationPanicCallable) Call(context.Context, []byte) (any, error) {
	t.callCount++
	return "must not execute", nil
}

type metadataPanicCallable struct {
	callCount int
}

func (*metadataPanicCallable) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "workspace_exec"}
}

func (t *metadataPanicCallable) Call(context.Context, []byte) (any, error) {
	t.callCount++
	return "must not execute", nil
}

func (*metadataPanicCallable) ToolMetadata() tool.ToolMetadata {
	panic("metadata secret")
}

type initialDeclarationPanicCallable struct{}

func (*initialDeclarationPanicCallable) Declaration() *tool.Declaration {
	panic("initial declaration secret")
}

func (*initialDeclarationPanicCallable) Call(context.Context, []byte) (any, error) {
	return nil, nil
}

func TestWrappedCallableMetadataAndDeclarationPanicsDoNotExecute(t *testing.T) {
	guard, err := New(testPolicy())
	require.NoError(t, err)

	_, err = WrapCallableTool(&initialDeclarationPanicCallable{}, guard)
	require.ErrorContains(t, err, "declaration panicked")
	require.NotContains(t, err.Error(), "secret")

	declarationTool := &declarationPanicCallable{}
	wrapped, err := WrapCallableTool(declarationTool, guard)
	require.NoError(t, err)
	_, err = wrapped.Call(context.Background(), []byte("{\"command\":\"go test ./...\"}"))
	require.ErrorContains(t, err, "declaration panicked")
	require.Zero(t, declarationTool.callCount)
	require.Nil(t, wrapped.Declaration())

	metadataTool := &metadataPanicCallable{}
	wrapped, err = WrapCallableTool(metadataTool, guard)
	require.NoError(t, err)
	metadata := tool.MetadataOf(wrapped)
	require.True(t, metadata.Destructive)
	require.True(t, metadata.OpenWorld)
	_, err = wrapped.Call(context.Background(), []byte("{\"command\":\"go test ./...\"}"))
	require.ErrorContains(t, err, "metadata panicked")
	require.Zero(t, metadataTool.callCount)
}

func TestOptionsAndTelemetryAcceptNilGuardOrContext(t *testing.T) {
	require.NotPanics(t, func() {
		WithAuditSink(nil)(nil)
		WithRedactor(nil)(nil)
		WithAuditErrorHook(nil)(nil)
		RecordSpan(nil, Report{})
	})
}

func TestReportMatchesAreBoundedAndPreserveStrongestDecision(t *testing.T) {
	manyMatches := func(includeDeny bool) []Match {
		matches := make([]Match, 0, maxReportMatches+50)
		for index := 0; index < maxReportMatches+49; index++ {
			matches = append(matches, Match{
				Decision:       tool.PermissionActionAllow,
				RiskLevel:      RiskLevelLow,
				RuleID:         fmt.Sprintf("custom.allow.%d", index),
				Evidence:       "bounded custom result",
				Recommendation: "continue",
			})
		}
		if includeDeny {
			matches = append(matches, Match{
				Decision:       tool.PermissionActionDeny,
				RiskLevel:      RiskLevelCritical,
				RuleID:         "custom.final_deny",
				Evidence:       "the strongest result appears after the report cap",
				Recommendation: "do not execute",
			})
		}
		return matches
	}

	guard, err := New(testPolicy(), WithRule(RuleFunc(func(
		context.Context,
		Request,
		Policy,
	) []Match {
		return manyMatches(true)
	})))
	require.NoError(t, err)
	report, err := guard.Scan(
		context.Background(),
		commandRequest(BackendWorkspace, "go test ./tool/safety"),
	)
	require.NoError(t, err)
	require.Len(t, report.Matches, maxReportMatches)
	require.Equal(t, tool.PermissionActionDeny, report.Decision)
	require.True(t, hasRule(report, "custom.final_deny"))
	require.True(t, hasRule(report, "limits.match_count"))

	guard, err = New(testPolicy(), WithRule(RuleFunc(func(
		context.Context,
		Request,
		Policy,
	) []Match {
		return manyMatches(false)
	})))
	require.NoError(t, err)
	report, err = guard.Scan(
		context.Background(),
		commandRequest(BackendWorkspace, "go test ./tool/safety"),
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, report.Decision)
	require.True(t, hasRule(report, "limits.match_count"))
}
