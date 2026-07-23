// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestJSONLAuditSinkWritesOneEventWithOneWrite(t *testing.T) {
	writer := &countingWriter{}
	sink := safety.NewJSONLAuditSink(writer)
	event := auditEvent("scan-one")

	require.NoError(t, sink.Record(context.Background(), event))
	require.Equal(t, 1, writer.writeCount())
	require.Equal(t, 1, strings.Count(writer.String(), "\n"))
}

func TestAuditEventJSONRoundTripAndOmitsEmptyExecutionStatus(t *testing.T) {
	var output bytes.Buffer
	sink := safety.NewJSONLAuditSink(&output)
	event := auditEvent("scan-round-trip")
	event.ExecutionStatus = "completed"

	require.NoError(t, sink.Record(context.Background(), event))

	var got safety.AuditEvent
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &got))
	require.Equal(t, event, got)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &fields))
	require.Equal(t, map[string]struct{}{
		"schema_version": {}, "timestamp": {}, "scan_id": {}, "stage": {},
		"tool_name": {}, "backend": {}, "decision": {}, "risk_level": {},
		"rule_id": {}, "duration_ms": {}, "redacted": {}, "intercepted": {},
		"execution_status": {},
	}, stringSet(fields))

	output.Reset()
	event.ExecutionStatus = ""
	require.NoError(t, sink.Record(context.Background(), event))
	fields = nil
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &fields))
	require.NotContains(t, fields, "execution_status")
}

func TestAuditEventDoesNotDefaultWireValues(t *testing.T) {
	var output bytes.Buffer
	sink := safety.NewJSONLAuditSink(&output)

	require.NoError(t, sink.Record(context.Background(), safety.AuditEvent{}))

	var got safety.AuditEvent
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &got))
	require.Equal(t, safety.AuditEvent{}, got)
}

func TestAuditFailureModeValues(t *testing.T) {
	require.Equal(t, safety.AuditFailureMode("best_effort"), safety.AuditBestEffort)
	require.Equal(t, safety.AuditFailureMode("required"), safety.AuditRequired)
}

func TestJSONLAuditSinkSerializesConcurrentRecords(t *testing.T) {
	const records = 64

	var output bytes.Buffer
	sink := safety.NewJSONLAuditSink(&output)
	var group sync.WaitGroup
	errs := make(chan error, records)
	for i := 0; i < records; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			errs <- sink.Record(context.Background(), auditEvent("scan-concurrent-"+strconv.Itoa(i)))
		}(i)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	require.Len(t, lines, records)
	seen := make(map[string]struct{}, records)
	for _, line := range lines {
		var event safety.AuditEvent
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		require.NotEmpty(t, event.ScanID)
		_, exists := seen[event.ScanID]
		require.False(t, exists, "duplicate scan ID %q", event.ScanID)
		seen[event.ScanID] = struct{}{}
	}
}

func TestJSONLAuditSinkRejectsNilWriter(t *testing.T) {
	sink := safety.NewJSONLAuditSink(nil)
	require.NotNil(t, sink)
	err := sink.Record(context.Background(), auditEvent("scan-nil-writer"))
	require.EqualError(t, err, "audit writer is nil")
}

func TestJSONLAuditSinkRejectsNilReceiver(t *testing.T) {
	var sink *safety.JSONLAuditSink
	err := sink.Record(context.Background(), auditEvent("scan-nil-receiver"))
	require.EqualError(t, err, "audit sink is nil")
}

func TestJSONLAuditSinkDoesNotWriteCancelledContext(t *testing.T) {
	writer := &countingWriter{}
	sink := safety.NewJSONLAuditSink(writer)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := sink.Record(ctx, auditEvent("scan-cancelled"))
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, writer.writeCount())
}

func TestJSONLAuditSinkWrapsWriterError(t *testing.T) {
	want := errors.New("disk full")
	sink := safety.NewJSONLAuditSink(errorWriter{err: want})

	err := sink.Record(context.Background(), auditEvent("scan-writer-error"))
	require.ErrorIs(t, err, want)
}

func TestAuditEventCannotContainRawOrSecretFields(t *testing.T) {
	allowed := map[string]struct{}{
		"SchemaVersion": {}, "Timestamp": {}, "ScanID": {}, "Stage": {},
		"ToolName": {}, "Backend": {}, "Decision": {}, "RiskLevel": {},
		"RuleID": {}, "DurationMillis": {}, "Redacted": {}, "Intercepted": {},
		"ExecutionStatus": {},
	}
	typ := reflect.TypeOf(safety.AuditEvent{})
	for i := 0; i < typ.NumField(); i++ {
		_, exists := allowed[typ.Field(i).Name]
		require.True(t, exists, "unexpected field %q", typ.Field(i).Name)
	}
	require.Equal(t, len(allowed), typ.NumField())

	encoded, err := json.Marshal(auditEvent("scan-secret-minimization"))
	require.NoError(t, err)
	for _, key := range []string{
		"command", "arguments", "args", "evidence", "environment", "env",
		"result", "artifact", "metadata",
	} {
		require.NotContains(t, string(encoded), "\""+key+"\"")
	}
}

func stringSet(values map[string]json.RawMessage) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for value := range values {
		set[value] = struct{}{}
	}
	return set
}

func auditEvent(scanID string) safety.AuditEvent {
	return safety.AuditEvent{
		SchemaVersion:   1,
		Timestamp:       time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
		ScanID:          scanID,
		Stage:           "pre_execution",
		ToolName:        "workspace_exec",
		Backend:         safety.BackendWorkspaceExec,
		Decision:        safety.DecisionDeny,
		RiskLevel:       safety.RiskHigh,
		RuleID:          "dangerous.rm_rf",
		DurationMillis:  12,
		Redacted:        true,
		Intercepted:     true,
		ExecutionStatus: "",
	}
}

type countingWriter struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	writes int
}

func (w *countingWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes++
	return w.buffer.Write(data)
}

func (w *countingWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

func (w *countingWriter) writeCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writes
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}
