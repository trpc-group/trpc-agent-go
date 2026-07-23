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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type fakeCallableTool struct {
	name   string
	output any
	err    error
	calls  int
}

func (f *fakeCallableTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: f.name}
}

func (f *fakeCallableTool) Call(context.Context, []byte) (any, error) {
	f.calls++
	return f.output, f.err
}

type fakeCodeExecutor struct {
	result codeexecutor.CodeExecutionResult
	calls  int
}

type failingAuditor struct{}

func (failingAuditor) Record(context.Context, AuditEvent) error {
	return errors.New("audit unavailable")
}

func (f *fakeCodeExecutor) ExecuteCode(
	context.Context,
	codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	f.calls++
	return f.result, nil
}

func (f *fakeCodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func TestGuardPermissionPolicyBlocksBeforeExecutionAndAudits(t *testing.T) {
	scanner, err := NewScanner(testPolicy())
	require.NoError(t, err)
	var audit bytes.Buffer
	guard := NewGuard(scanner, WithAuditor(NewJSONLAuditor(&audit)))
	tl := &fakeCallableTool{name: "workspace_exec"}

	decision, err := guard.CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{
			Tool:        tl,
			ToolName:    "workspace_exec",
			Arguments:   []byte(`{"command":"rm -rf /"}`),
			Declaration: tl.Declaration(),
		},
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, RuleDangerousDelete)

	var event AuditEvent
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(audit.Bytes()), &event))
	require.Equal(t, "workspace_exec", event.ToolName)
	require.Equal(t, BackendWorkspace, event.Backend)
	require.Equal(t, DecisionDeny, event.Decision)
	require.Equal(t, RiskCritical, event.RiskLevel)
	require.Equal(t, RuleDangerousDelete, event.RuleID)
	require.True(t, event.Blocked)
	require.GreaterOrEqual(t, event.DurationMicros, int64(0))
	require.NotEmpty(t, event.CommandSHA256)
}

func TestGuardPermissionPolicyRedactsSensitiveAuditData(t *testing.T) {
	scanner, err := NewScanner(testPolicy())
	require.NoError(t, err)
	var audit bytes.Buffer
	guard := NewGuard(scanner, WithAuditor(NewJSONLAuditor(&audit)))
	const secret = "token=abcdefghijklmnop"

	decision, err := guard.CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{
			ToolName:  "workspace_exec",
			Arguments: []byte(`{"command":"echo ` + secret + `"}`),
		},
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.NotContains(t, audit.String(), secret)

	var event AuditEvent
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(audit.Bytes()), &event))
	require.True(t, event.Redacted)
	require.Equal(t, RuleSensitiveLiteral, event.RuleID)
}

func TestGuardSetsOpenTelemetrySafetyAttributes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(recorder),
	)
	defer func() {
		require.NoError(t, provider.Shutdown(context.Background()))
	}()
	ctx, span := provider.Tracer("test").Start(context.Background(), "scan")

	scanner, err := NewScanner(testPolicy())
	require.NoError(t, err)
	guard := NewGuard(scanner)
	_, err = guard.CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName:  "exec_command",
		Arguments: []byte(`{"command":"rm -rf /"}`),
	})
	require.NoError(t, err)
	span.End()

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	attributes := make(map[string]string)
	for _, attr := range ended[0].Attributes() {
		attributes[string(attr.Key)] = attr.Value.AsString()
	}
	require.Equal(t, string(DecisionDeny), attributes[AttrDecision])
	require.Equal(t, string(RiskCritical), attributes[AttrRiskLevel])
	require.Equal(t, RuleDangerousDelete, attributes[AttrRuleID])
	require.Equal(t, string(BackendHost), attributes[AttrBackend])
}

func TestToolWrapperBlocksAndSanitizesResults(t *testing.T) {
	scanner, err := NewScanner(testPolicy())
	require.NoError(t, err)
	guard := NewGuard(scanner)

	denied := &fakeCallableTool{name: "exec_command"}
	wrappedDenied := WrapTool(denied, guard, BackendHost)
	result, err := wrappedDenied.Call(
		context.Background(),
		[]byte(`{"command":"rm -rf /"}`),
	)
	require.NoError(t, err)
	require.Zero(t, denied.calls)
	permissionResult, ok := result.(tool.PermissionResult)
	require.True(t, ok)
	require.Equal(t, tool.PermissionResultStatusDenied, permissionResult.Status)

	allowed := &fakeCallableTool{
		name: "workspace_exec",
		output: map[string]any{
			"output": "password=hunter2",
		},
	}
	wrappedAllowed := WrapTool(allowed, guard, BackendWorkspace)
	result, err = wrappedAllowed.Call(
		context.Background(),
		[]byte(`{"command":"go test ./..."}`),
	)
	require.NoError(t, err)
	require.Equal(t, 1, allowed.calls)
	output, ok := result.(map[string]any)
	require.True(t, ok)
	require.NotContains(t, output["output"], "hunter2")
	require.Contains(t, output["output"], redactedValue)
}

func TestCodeExecutorWrapperBlocksAndSanitizesResults(t *testing.T) {
	scanner, err := NewScanner(testPolicy())
	require.NoError(t, err)
	guard := NewGuard(scanner)
	next := &fakeCodeExecutor{
		result: codeexecutor.CodeExecutionResult{
			Output: "api_key=abcdefghijklmnop",
			OutputFiles: []codeexecutor.File{{
				Name:     "result.txt",
				Content:  "password=hunter2",
				MIMEType: "text/plain",
			}},
		},
	}
	wrapped := WrapCodeExecutor(next, guard, BackendLocal)

	_, err = wrapped.ExecuteCode(
		context.Background(),
		codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{
				Language: "bash",
				Code:     "rm -rf /",
			}},
		},
	)
	var blocked *BlockedError
	require.True(t, errors.As(err, &blocked))
	require.Equal(t, RuleDangerousDelete, blocked.Report.RuleID)
	require.Zero(t, next.calls)

	result, err := wrapped.ExecuteCode(
		context.Background(),
		codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{
				Language: "bash",
				Code:     "go test ./...",
			}},
		},
	)
	require.NoError(t, err)
	require.Equal(t, 1, next.calls)
	require.False(t, strings.Contains(result.Output, "abcdefghijklmnop"))
	require.Contains(t, result.Output, redactedValue)
	require.False(t, strings.Contains(result.OutputFiles[0].Content, "hunter2"))
	require.Contains(t, result.OutputFiles[0].Content, redactedValue)
	require.Equal(
		t,
		next.CodeBlockDelimiter(),
		wrapped.CodeBlockDelimiter(),
	)
}

func TestGuardUsesMetadataForNonExecutionTools(t *testing.T) {
	scanner, err := NewScanner(testPolicy())
	require.NoError(t, err)
	guard := NewGuard(scanner)

	decision, err := guard.CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{
			ToolName: "read_inventory",
			Metadata: tool.ToolMetadata{ReadOnly: true},
		},
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)

	decision, err = guard.CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{
			ToolName: "delete_inventory",
			Metadata: tool.ToolMetadata{Destructive: true},
		},
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, RuleMetadataDestructive)

	decision, err = guard.CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{
			ToolName: "remote_mcp_tool",
			Metadata: tool.ToolMetadata{OpenWorld: true},
		},
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, RuleMetadataOpenWorld)
}

func TestToolWrapperAppliesAggregateOutputLimitAndRedactsErrors(t *testing.T) {
	policy := testPolicy()
	policy.Limits.MaxOutputBytes = 16
	scanner, err := NewScanner(policy)
	require.NoError(t, err)
	guard := NewGuard(scanner)
	next := &fakeCallableTool{
		name: "workspace_exec",
		output: map[string]any{
			"first":  strings.Repeat("a", 32),
			"second": strings.Repeat("b", 32),
		},
		err: errors.New("password=hunter2"),
	}

	result, callErr := WrapTool(
		next,
		guard,
		BackendWorkspace,
	).Call(
		context.Background(),
		[]byte(`{"command":"go test ./..."}`),
	)
	require.Error(t, callErr)
	require.NotContains(t, callErr.Error(), "hunter2")
	output := result.(map[string]any)
	combined := output["first"].(string) + output["second"].(string)
	require.Contains(t, combined, truncatedValue)
	require.Less(t, len(combined), 128)
}

func TestToolWrapperRedactsSensitiveMapKeys(t *testing.T) {
	scanner, err := NewScanner(testPolicy())
	require.NoError(t, err)
	guard := NewGuard(scanner)
	next := &fakeCallableTool{
		name: "workspace_exec",
		output: map[string]string{
			"password=hunter2": "ok",
		},
	}

	result, err := WrapTool(
		next,
		guard,
		BackendWorkspace,
	).Call(
		context.Background(),
		[]byte(`{"command":"go test ./..."}`),
	)
	require.NoError(t, err)
	output := result.(map[string]string)
	require.Len(t, output, 1)
	for key := range output {
		require.NotContains(t, key, "hunter2")
		require.Contains(t, key, redactedValue)
	}
}

func TestGuardTreatsAuditFailuresAsBestEffort(t *testing.T) {
	scanner, err := NewScanner(testPolicy())
	require.NoError(t, err)
	guard := NewGuard(scanner, WithAuditor(failingAuditor{}))

	decision, err := guard.CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{
			ToolName:  "workspace_exec",
			Arguments: []byte(`{"command":"go test ./..."}`),
		},
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)

	nextTool := &fakeCallableTool{
		name:   "workspace_exec",
		output: "completed",
	}
	output, err := WrapTool(
		nextTool,
		guard,
		BackendWorkspace,
	).Call(
		context.Background(),
		[]byte(`{"command":"go test ./..."}`),
	)
	require.NoError(t, err)
	require.Equal(t, "completed", output)
	require.Equal(t, 1, nextTool.calls)

	nextExecutor := &fakeCodeExecutor{
		result: codeexecutor.CodeExecutionResult{Output: "completed"},
	}
	result, err := WrapCodeExecutor(
		nextExecutor,
		guard,
		BackendLocal,
	).ExecuteCode(
		context.Background(),
		codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{
				Language: "bash",
				Code:     "go test ./...",
			}},
		},
	)
	require.NoError(t, err)
	require.Equal(t, "completed", result.Output)
	require.Equal(t, 1, nextExecutor.calls)
}

func TestDecodeCodeBlocksBoundsNestedJSONStrings(t *testing.T) {
	raw := json.RawMessage(
		`[{"language":"bash","code":"go test ./..."}]`,
	)
	for i := 0; i < maxCodeBlockUnwrapDepth+1; i++ {
		encoded, err := json.Marshal(string(raw))
		require.NoError(t, err)
		raw = encoded
	}

	_, err := decodeCodeBlocks(raw)
	require.ErrorContains(t, err, "nested JSON strings")
}

func TestReportJSONContainsRequiredContractFields(t *testing.T) {
	scanner, err := NewScanner(testPolicy())
	require.NoError(t, err)
	report := scanner.Scan(context.Background(), Input{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspace,
		Command:  "go test ./...",
	})
	data, err := json.Marshal(report)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(data, &fields))
	for _, name := range []string{
		"decision",
		"risk_level",
		"rule_id",
		"evidence",
		"recommendation",
		"tool_name",
		"backend",
		"blocked",
	} {
		require.Contains(t, fields, name)
	}
}

func TestJSONLAuditorSupportsConcurrentWriters(t *testing.T) {
	var output bytes.Buffer
	auditor := NewJSONLAuditor(&output)
	const count = 50
	var wg sync.WaitGroup
	errs := make(chan error, count)
	wg.Add(count)
	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			errs <- auditor.Record(
				context.Background(),
				AuditEvent{
					ToolName: "workspace_exec",
					Decision: DecisionAllow,
					RuleID:   RuleAllow,
				},
			)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Len(t, strings.Split(strings.TrimSpace(output.String()), "\n"), count)
}

func TestJSONLAuditorHonorsCanceledContext(t *testing.T) {
	var output bytes.Buffer
	auditor := NewJSONLAuditor(&output)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := auditor.Record(ctx, AuditEvent{RuleID: RuleAllow})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, output.String())
}
