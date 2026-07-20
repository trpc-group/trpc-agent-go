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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAuditWriter_OneJSONPerLine(t *testing.T) {
	buf := new(bytes.Buffer)
	w := NewAuditWriterFrom(buf, false, true)
	events := []AuditEvent{
		{ScanID: "a", ToolName: "workspace_exec", Decision: DecisionAllow},
		{ScanID: "b", ToolName: "workspace_exec", Decision: DecisionDeny},
		{ScanID: "c", ToolName: "workspace_exec", Decision: DecisionAsk},
	}
	for _, ev := range events {
		require.NoError(t, w.Append(ev))
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3)
	for _, line := range lines {
		require.NotEmpty(t, line)
		var ev AuditEvent
		require.NoError(t, json.Unmarshal([]byte(line), &ev))
	}
}

func TestAuditWriter_ConcurrentAppendsDoNotInterleave(t *testing.T) {
	buf := new(bytes.Buffer)
	w := NewAuditWriterFrom(buf, false, true)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			require.NoError(t, w.Append(AuditEvent{
				ScanID:   "scan-" + itoa(n),
				ToolName: "workspace_exec",
				Decision: DecisionAllow,
			}))
		}(i)
	}
	wg.Wait()
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 50)
	for _, line := range lines {
		require.NotEmpty(t, line)
		var ev AuditEvent
		require.NoError(t, json.Unmarshal([]byte(line), &ev))
	}
}

func TestAuditWriter_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := NewAuditWriter(path, false, true)
	require.NoError(t, err)
	require.NoError(t, w.Append(AuditEvent{ScanID: "x", ToolName: "t"}))
	require.NoError(t, w.Close())
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestAuditWriter_RequiredFailureSurfacesError(t *testing.T) {
	w := NewAuditWriterFrom(&failingWriter{}, true, true)
	err := w.Append(AuditEvent{ScanID: "x"})
	require.Error(t, err)
}

func TestAuditWriter_NonRequiredFailureIsSilent(t *testing.T) {
	w := NewAuditWriterFrom(&failingWriter{}, false, true)
	err := w.Append(AuditEvent{ScanID: "x"})
	require.NoError(t, err)
}

func TestAuditWriter_CloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := NewAuditWriter(path, false, true)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	require.NoError(t, w.Close())
}

func TestAuditEvent_HasNoSecret(t *testing.T) {
	ev := AuditEvent{
		ScanID:      "scan-1",
		ToolName:    "workspace_exec",
		Decision:    DecisionDeny,
		RiskLevel:   RiskCritical,
		RuleIDs:     []string{"secret.input_or_code"},
		DurationMs:  0.5,
		Redacted:    true,
		Intercepted: true,
		CommandHash: "abc123",
		Timestamp:   time.Now().UTC(),
	}
	raw, err := json.Marshal(ev)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "sk_live_")
	require.NotContains(t, string(raw), "AKIA")
}
