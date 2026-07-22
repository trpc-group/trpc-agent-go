//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestJSONLAuditSinkConcurrentWritesAreCompleteAndRedacted(t *testing.T) {
	const (
		writers         = 16
		eventsPerWriter = 25
		secret          = "audit-secret-value"
	)
	var output bytes.Buffer
	sink := NewJSONLAuditSink(&output)
	var group sync.WaitGroup

	for writer := 0; writer < writers; writer++ {
		group.Add(1)
		go func(writer int) {
			defer group.Done()
			for index := 0; index < eventsPerWriter; index++ {
				err := sink.WriteAudit(context.Background(), AuditEvent{
					ToolName:       "workspace_exec",
					ToolCallID:     fmt.Sprintf("call-%d-%d", writer, index),
					Backend:        BackendWorkspace,
					Decision:       tool.PermissionActionDeny,
					RiskLevel:      RiskLevelCritical,
					RuleID:         "credential.read",
					Evidence:       "password=" + secret,
					Recommendation: "remove --token " + secret,
					Blocked:        true,
					DurationMS:     2,
					CommandPreview: "tool --token " + secret,
				})
				require.NoError(t, err)
			}
		}(writer)
	}
	group.Wait()

	raw := output.String()
	require.NotContains(t, raw, secret)
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	require.Len(t, lines, writers*eventsPerWriter)
	for _, line := range lines {
		require.True(t, json.Valid([]byte(line)), line)
		var event AuditEvent
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		require.Equal(t, auditSchemaVersion, event.SchemaVersion)
		require.False(t, event.Timestamp.IsZero())
		require.True(t, event.Blocked)
		require.True(t, event.Redacted)
		require.Positive(t, event.RedactionCount)
	}
}

func TestJSONLAuditSinkRejectsNilWriter(t *testing.T) {
	err := NewJSONLAuditSink(nil).WriteAudit(context.Background(), AuditEvent{})
	require.ErrorContains(t, err, "writer is nil")
}

func TestJSONLAuditSinkReturnsWriterFailure(t *testing.T) {
	wantErr := errors.New("disk unavailable")
	sink := NewJSONLAuditSink(failingWriter{err: wantErr})
	err := sink.WriteAudit(context.Background(), AuditEvent{ToolName: "workspace_exec"})
	require.ErrorIs(t, err, wantErr)
}

func TestSanitizeReportRedactsEveryHumanReadableFindingField(t *testing.T) {
	const secret = "report-secret-value"
	report := Report{
		Decision:       tool.PermissionActionDeny,
		RiskLevel:      RiskLevelCritical,
		RuleID:         "credential.read",
		ToolName:       "workspace_exec",
		Command:        "run --token " + secret,
		Evidence:       "password=" + secret,
		Recommendation: "remove api_key=" + secret,
		Matches: []Match{{
			Decision:       tool.PermissionActionDeny,
			RiskLevel:      RiskLevelCritical,
			RuleID:         "credential.read",
			Evidence:       "Bearer " + secret,
			Recommendation: "rotate token=" + secret,
		}},
	}

	clean := sanitizeReport(NewRedactor(), report)
	encoded, err := json.Marshal(clean)

	require.NoError(t, err)
	require.NotContains(t, string(encoded), secret)
	require.True(t, clean.Redacted)
	require.Contains(t, string(encoded), RedactedValue)
}

func TestWriteGuardAuditEmitsHashAndSanitizedPreview(t *testing.T) {
	const secret = "guard-audit-secret"
	request := Request{
		ToolName:   "workspace_exec",
		ToolCallID: "call-1",
		Backend:    BackendWorkspace,
		Command:    "go test ./... --token " + secret,
	}
	report := sanitizeReport(NewRedactor(), Report{
		Decision:   tool.PermissionActionAsk,
		RiskLevel:  RiskLevelMedium,
		RuleID:     "command.review",
		Evidence:   "token=" + secret,
		ToolName:   request.ToolName,
		Command:    request.Command,
		Backend:    request.Backend,
		Blocked:    true,
		DurationMS: 7,
		Matches: []Match{
			{RuleID: "command.review"},
			{RuleID: "secret.output"},
			{RuleID: "command.review"},
		},
	})
	var got AuditEvent
	sink := AuditSinkFunc(func(_ context.Context, event AuditEvent) error {
		got = event
		return nil
	})

	err := writeGuardAudit(context.Background(), sink, request, report)
	encoded, marshalErr := json.Marshal(got)

	require.NoError(t, err)
	require.NoError(t, marshalErr)
	require.NotContains(t, string(encoded), secret)
	require.Equal(t, hashText(request.Command), got.CommandSHA256)
	require.Equal(t, []string{"command.review", "secret.output"}, got.RuleIDs)
	require.Equal(t, int64(7), got.DurationMS)
	require.True(t, got.Redacted)
}

func TestJSONLAuditSinkNormalizesTimestampToUTC(t *testing.T) {
	var output bytes.Buffer
	sink := NewJSONLAuditSink(&output)
	location := time.FixedZone("test", 8*60*60)
	want := time.Date(2026, 7, 16, 12, 30, 0, 0, location).UTC()

	require.NoError(t, sink.WriteAudit(context.Background(), AuditEvent{
		Timestamp: time.Date(2026, 7, 16, 12, 30, 0, 0, location),
	}))

	var got AuditEvent
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &got))
	require.Equal(t, want, got.Timestamp)
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}
