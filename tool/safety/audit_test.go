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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	gootel "go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestAuditIsRedactedJSONL(t *testing.T) {
	const secret = "sk-1234567890abcdef1234"
	var output bytes.Buffer
	report, err := newTestScanner(t, WithAuditWriter(&output)).Scan(
		context.Background(),
		ScanInput{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "echo API_KEY=" + secret,
		},
	)
	require.NoError(t, err)
	require.True(t, report.Redacted)
	require.NotContains(t, report.Command, secret)
	require.NotContains(t, output.String(), secret)

	var event auditEvent
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event))
	require.Equal(t, RuleSecretExposure, event.RuleID)
	require.Equal(t, currentSchemaVersion, event.SchemaVersion)
	require.Equal(t, report.PolicyID, event.PolicyID)
	require.Equal(t, report.PolicyRevision, event.PolicyRevision)
	require.Equal(t, DecisionDeny, event.Decision)
	require.True(t, event.Redacted)
	require.True(t, event.ExecutionBlocked)
	require.NotEmpty(t, event.CommandSHA256)
}

func TestAuditSanitizesToolNameAndBackend(t *testing.T) {
	const secret = "sk-1234567890abcdef1234"
	toolName := strings.Repeat("runner", maxReportTextBytes) + secret
	var output bytes.Buffer
	report, err := newTestScanner(t, WithAuditWriter(&output)).Scan(
		context.Background(),
		ScanInput{
			ToolName: toolName,
			Backend:  Backend(secret),
			Command:  "echo ok",
		},
	)
	require.NoError(t, err)
	require.True(t, report.Redacted)
	require.NotContains(t, report.ToolName, secret)
	require.LessOrEqual(t, len(report.ToolName), maxReportTextBytes)
	require.Equal(t, BackendUnknown, report.Backend)
	require.NotContains(t, output.String(), secret)

	var event auditEvent
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event))
	require.NotContains(t, event.ToolName, secret)
	require.Equal(t, BackendUnknown, event.Backend)
}

func TestAuditConcurrentWritesRemainWholeLines(t *testing.T) {
	var output bytes.Buffer
	scanner := newTestScanner(t, WithAuditWriter(&output))
	const count = 50
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := scanner.Scan(context.Background(), ScanInput{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspace,
				Command:  "go test ./...",
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	lines := 0
	lineScanner := bufio.NewScanner(bytes.NewReader(output.Bytes()))
	for lineScanner.Scan() {
		lines++
		var event auditEvent
		require.NoError(t, json.Unmarshal(lineScanner.Bytes(), &event))
	}
	require.NoError(t, lineScanner.Err())
	require.Equal(t, count, lines)
}

func TestAuditFailureFailsClosed(t *testing.T) {
	scanner := newTestScanner(t, WithAuditWriter(errorWriter{}))
	report, err := scanner.Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspace,
		Command:  "go test ./...",
	})
	require.Error(t, err)
	require.Equal(t, DecisionAllow, report.Decision)

	decision, err := scanner.CheckToolPermission(
		context.Background(),
		permissionRequest(
			"workspace_exec",
			map[string]*tool.Schema{
				"command": {Type: "string"},
				"cwd":     {Type: "string"},
			},
			`{"command":"go test ./..."}`,
		),
	)
	require.Error(t, err)
	require.Empty(t, decision.Action)
}

func TestNewScannerRejectsNilAuditWriter(t *testing.T) {
	_, err := NewScanner(testPolicy(), WithAuditWriter(nil))
	require.ErrorContains(t, err, "nil audit writer")

	var output *bytes.Buffer
	_, err = NewScanner(testPolicy(), WithAuditWriter(output))
	require.ErrorContains(t, err, "nil audit writer")
}

func TestScanSetsOTelAttributes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	oldProvider := gootel.GetTracerProvider()
	gootel.SetTracerProvider(provider)
	t.Cleanup(func() { gootel.SetTracerProvider(oldProvider) })

	ctx, span := provider.Tracer("test").Start(context.Background(), "scan")
	_, err := newTestScanner(t).Scan(ctx, ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspace,
		Command:  "rm -rf /",
	})
	require.NoError(t, err)
	span.End()

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	attributes := make(map[string]string)
	for _, item := range ended[0].Attributes() {
		attributes[string(item.Key)] = item.Value.AsString()
	}
	require.Equal(t, "deny", attributes["tool.safety.decision"])
	require.Equal(t, currentSchemaVersion, attributes["tool.safety.schema_version"])
	require.Equal(t, defaultPolicyID, attributes["tool.safety.policy_id"])
	require.Len(t, attributes["tool.safety.policy_revision"], 64)
	require.Equal(t, "critical", attributes["tool.safety.risk_level"])
	require.Equal(t, string(RuleDangerousDelete), attributes["tool.safety.rule_id"])
	require.Equal(t, string(BackendWorkspace), attributes["tool.safety.backend"])

	const secret = "sk-1234567890abcdef1234"
	ctx, span = provider.Tracer("test").Start(context.Background(), "invalid-backend")
	_, err = newTestScanner(t).Scan(ctx, ScanInput{
		ToolName: "runner",
		Backend:  Backend(secret),
		Command:  "echo ok",
	})
	require.NoError(t, err)
	span.End()

	ended = recorder.Ended()
	require.Len(t, ended, 2)
	attributes = make(map[string]string)
	for _, item := range ended[1].Attributes() {
		attributes[string(item.Key)] = item.Value.AsString()
		require.NotContains(t, item.Value.AsString(), secret)
	}
	require.Equal(t, string(BackendUnknown), attributes["tool.safety.backend"])
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("audit unavailable")
}
