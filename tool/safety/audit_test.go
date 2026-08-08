//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestAuditWriter_RejectsSymlinkPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	require.NoError(t, os.WriteFile(target, []byte("safe"), 0o600))
	link := filepath.Join(dir, "audit.jsonl")
	require.NoError(t, os.Symlink(target, link))
	_, err := NewAuditWriter(link, true, true)
	require.Error(t, err)
	require.Equal(t, "safe", string(requireFile(t, target)))
}

func requireFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
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

// TestAuditWriter_CloseErrorHonorsRequired verifies the Close contract
// documented on AuditWriter: flush and close failures surface only when
// the writer is required; a best-effort writer never fails Close.
func TestAuditWriter_CloseErrorHonorsRequired(t *testing.T) {
	// A non-required writer swallows the closer error.
	w := &AuditWriter{
		w:      new(bytes.Buffer),
		bw:     bufio.NewWriter(new(bytes.Buffer)),
		closer: covercoreFailingCloser{},
	}
	require.NoError(t, w.Close())

	// A required writer surfaces the same closer error.
	wReq := &AuditWriter{
		w:        new(bytes.Buffer),
		bw:       bufio.NewWriter(new(bytes.Buffer)),
		closer:   covercoreFailingCloser{},
		required: true,
	}
	require.Error(t, wReq.Close())
}

// TestAuditWriter_AppendAfterClose covers the closed-writer contract:
// a required writer fails while an optional writer silently no-ops.
func TestAuditWriter_AppendAfterClose(t *testing.T) {
	w := NewAuditWriterFrom(new(bytes.Buffer), true, true)
	require.NoError(t, w.Close())
	require.Error(t, w.Append(AuditEvent{}))

	buf := new(bytes.Buffer)
	w2 := NewAuditWriterFrom(buf, false, true)
	require.NoError(t, w2.Close())
	require.NoError(t, w2.Append(AuditEvent{}))
	require.Empty(t, buf.String())
}

func TestAuditWriter_StructuredRedactionKeepsValidJSON(t *testing.T) {
	buf := new(bytes.Buffer)
	w := NewAuditWriterFrom(buf, true, true)
	require.NoError(t, w.Append(AuditEvent{
		ToolName: "password: hunter2xyz",
		RuleIDs:  []string{"AKIAIOSFODNN7EXAMPLE"},
	}))
	line := strings.TrimSpace(buf.String())
	var event AuditEvent
	require.NoError(t, json.Unmarshal([]byte(line), &event))
	require.True(t, event.Redacted)
	require.NotContains(t, line, "hunter2xyz")
	require.NotContains(t, line, "AKIAIOSFODNN7EXAMPLE")
}

func TestAuditWriter_NilInjectedWriterFailsWithoutPanic(t *testing.T) {
	required := NewAuditWriterFrom(nil, true, true)
	require.Error(t, required.Append(AuditEvent{}))
	require.Error(t, required.Close())

	optional := NewAuditWriterFrom(nil, false, true)
	require.NoError(t, optional.Append(AuditEvent{}))
	require.NoError(t, optional.Close())
}

type syncingBuffer struct {
	bytes.Buffer
	syncs int
	err   error
}

func (b *syncingBuffer) Sync() error {
	b.syncs++
	return b.err
}

func TestAuditWriter_RequiredAppendSyncs(t *testing.T) {
	buf := &syncingBuffer{}
	w := NewAuditWriterFrom(buf, true, true)
	require.NoError(t, w.Append(AuditEvent{ToolName: "tool"}))
	require.Equal(t, 1, buf.syncs)
}
