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
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type memoryAuditor struct {
	mu     sync.Mutex
	events []AuditEvent
	err    error
}

func (auditor *memoryAuditor) Record(
	_ context.Context,
	event AuditEvent,
) error {
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	if auditor.err != nil {
		return auditor.err
	}
	auditor.events = append(auditor.events, event)
	return nil
}

func TestGuardScanRecordsRedactedAuditEvent(t *testing.T) {
	auditor := &memoryAuditor{}
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(auditor))
	require.NoError(t, err)

	report, err := guard.Scan(context.Background(), ScanInput{
		ToolName:   "workspace_exec",
		Command:    "echo api_key=top-secret-value",
		Backend:    BackendWorkspaceExec,
		Operation:  OperationExecute,
		Timeout:    DefaultPolicy().maxTimeout,
		WorkingDir: ".",
	})
	require.NoError(t, err)
	require.True(t, report.Redacted)
	require.NotContains(t, report.Command, "top-secret-value")
	require.Len(t, auditor.events, 1)
	require.Equal(t, auditPhasePrecheck, auditor.events[0].Phase)
	require.Equal(t, report.Decision, auditor.events[0].Decision)
	require.Equal(t, report.Blocked, auditor.events[0].Blocked)
	require.True(t, auditor.events[0].Timestamp.Location() == nil ||
		auditor.events[0].Timestamp.Location().String() == "UTC")
}

func TestGuardScanPropagatesRemoteProvider(t *testing.T) {
	auditor := &memoryAuditor{}
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(auditor))
	require.NoError(t, err)
	input := scanCommand("go env")
	input.Backend = BackendRemoteSandbox
	input.Provider = ProviderE2B

	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	require.Equal(t, ProviderE2B, report.Provider)
	require.Len(t, auditor.events, 1)
	require.Equal(t, ProviderE2B, auditor.events[0].Provider)
}

func TestGuardScanFailsClosedWhenAuditFails(t *testing.T) {
	auditor := &memoryAuditor{err: errors.New("disk full")}
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(auditor))
	require.NoError(t, err)

	report, err := guard.Scan(context.Background(), scanCommand("go test ./..."))
	require.ErrorContains(t, err, "record audit event")
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, "AUDIT_WRITE_FAILED", report.RuleID)
	require.True(t, report.Blocked)
}

func TestNewGuardRejectsTypedNilAuditor(t *testing.T) {
	var auditor *memoryAuditor
	_, err := NewGuard(DefaultPolicy(), WithAuditor(auditor))
	require.ErrorContains(t, err, "nil auditor")
}

func TestJSONLAuditorAppendsCompleteEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	auditor, err := NewJSONLAuditor(path)
	require.NoError(t, err)

	want := AuditEvent{
		Phase:     auditPhasePrecheck,
		ToolName:  "execute_e2b",
		Backend:   BackendRemoteSandbox,
		Provider:  ProviderE2B,
		Decision:  DecisionDeny,
		RiskLevel: RiskLevelCritical,
		RuleID:    "CMD_DANGEROUS_DELETE",
		Blocked:   true,
	}
	require.NoError(t, auditor.Record(context.Background(), want))
	require.NoError(t, auditor.Close())
	require.Error(t, auditor.Close())

	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	scanner := bufio.NewScanner(file)
	require.True(t, scanner.Scan())
	var got AuditEvent
	require.NoError(t, json.Unmarshal(scanner.Bytes(), &got))
	require.Equal(t, want.Phase, got.Phase)
	require.Equal(t, want.RuleID, got.RuleID)
	require.Equal(t, want.Provider, got.Provider)
	require.True(t, got.Blocked)
	require.False(t, scanner.Scan())
	require.NoError(t, scanner.Err())
}

func TestNewJSONLAuditorDoesNotCreateParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "audit.jsonl")
	_, err := NewJSONLAuditor(path)
	require.Error(t, err)
}

func TestNewJSONLAuditorWritableExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("existing\n"), 0o644))
	if runtime.GOOS != "windows" {
		require.NoError(t, os.Chmod(path, 0o644))
	}
	auditor, err := NewJSONLAuditor(path)
	require.NoError(t, err)
	require.NotNil(t, auditor)
	require.NoError(t, auditor.Close())
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(path)
		require.NoError(t, statErr)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestJSONLAuditorRejectsInvalidLifecycleUse(t *testing.T) {
	_, err := NewJSONLAuditor("")
	require.ErrorContains(t, err, "audit path is empty")

	var nilAuditor *JSONLAuditor
	require.ErrorContains(t,
		nilAuditor.Record(context.Background(), AuditEvent{}),
		"nil JSONL auditor",
	)
	require.ErrorContains(t, nilAuditor.Close(), "nil JSONL auditor")

	auditor, err := NewJSONLAuditor(filepath.Join(t.TempDir(), "audit.jsonl"))
	require.NoError(t, err)
	require.NoError(t, auditor.Close())
	require.ErrorContains(t,
		auditor.Record(context.Background(), AuditEvent{}),
		"JSONL auditor is closed",
	)
	require.ErrorContains(t, auditor.Close(), "JSONL auditor is closed")
}

func TestJSONLAuditorPropagatesIOErrors(t *testing.T) {
	t.Run("record", func(t *testing.T) {
		auditor, err := NewJSONLAuditor(filepath.Join(t.TempDir(), "audit.jsonl"))
		require.NoError(t, err)
		require.NoError(t, auditor.file.Close())
		err = auditor.Record(context.Background(), AuditEvent{})
		require.ErrorContains(t, err, "write audit event")
	})

	t.Run("close", func(t *testing.T) {
		auditor, err := NewJSONLAuditor(filepath.Join(t.TempDir(), "audit.jsonl"))
		require.NoError(t, err)
		require.NoError(t, auditor.file.Close())
		require.ErrorContains(t, auditor.Close(), "close audit file")
	})
}
