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
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCovercore_NewAuditWriterErrors covers the constructor error paths.
func TestCovercore_NewAuditWriterErrors(t *testing.T) {
	_, err := NewAuditWriter("", false, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")

	_, err = NewAuditWriter(t.TempDir()+"/missing/audit.jsonl", false, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "open audit path")
}

// TestCovercore_AuditWriterNilReceiver covers the nil-receiver no-ops.
func TestCovercore_AuditWriterNilReceiver(t *testing.T) {
	var w *AuditWriter
	require.NoError(t, w.Append(AuditEvent{ScanID: "x"}))
	require.NoError(t, w.Close())
	require.NoError(t, w.appendPreflight(ScanReport{ScanID: "x"}))
	require.NoError(t, w.appendPostExecute(ScanEvent{ScanID: "x"}, 0, false, "ok"))
}

// TestCovercore_AuditAppendOnClosedWriter covers the closed-writer
// branches for required and non-required writers.
func TestCovercore_AuditAppendOnClosedWriter(t *testing.T) {
	buf := new(bytes.Buffer)
	w := NewAuditWriterFrom(buf, false, true)
	require.NoError(t, w.Close())
	require.NoError(t, w.Append(AuditEvent{ScanID: "after-close"}))
	require.NotContains(t, buf.String(), "after-close")

	wReq := NewAuditWriterFrom(new(bytes.Buffer), true, true)
	require.NoError(t, wReq.Close())
	require.Error(t, wReq.Append(AuditEvent{ScanID: "after-close"}))
}

// TestCovercore_AuditAppendDefaults covers the schema-version and
// timestamp defaulting in Append.
func TestCovercore_AuditAppendDefaults(t *testing.T) {
	buf := new(bytes.Buffer)
	w := NewAuditWriterFrom(buf, false, false)
	require.NoError(t, w.Append(AuditEvent{ScanID: "s1", ToolName: "t"}))

	var ev AuditEvent
	require.NoError(t, json.Unmarshal(
		[]byte(strings.TrimSpace(buf.String())), &ev))
	require.Equal(t, "1", ev.SchemaVersion)
	require.False(t, ev.Timestamp.IsZero())
}

// TestCovercore_AuditAppendRedactsSecrets verifies that a secret placed in
// an event field never reaches the underlying writer when redaction is
// enabled.
func TestCovercore_AuditAppendRedactsSecrets(t *testing.T) {
	buf := new(bytes.Buffer)
	w := NewAuditWriterFrom(buf, false, true)
	require.NoError(t, w.Append(AuditEvent{
		ScanID:   "s1",
		ToolName: "t",
		// smuggle a secret into a field that should never carry one
		SessionHash: "AKIAIOSFODNN7EXAMPLE",
	}))
	require.NotContains(t, buf.String(), "AKIAIOSFODNN7EXAMPLE")
	require.Contains(t, buf.String(), "[REDACTED:")
}

// TestCovercore_AuditCloseFlushAndCloseErrors covers the Close error
// combinations using hand-built writers.
func TestCovercore_AuditCloseFlushAndCloseErrors(t *testing.T) {
	// Failing sink with pending buffered data: the flush fails and the
	// required writer surfaces the error.
	w := &AuditWriter{
		w:        &failingWriter{},
		bw:       bufio.NewWriter(&failingWriter{}),
		required: true,
	}
	_, err := w.bw.WriteString("pending")
	require.NoError(t, err)
	require.Error(t, w.Close())

	// Non-required writer swallows the flush error.
	w2 := &AuditWriter{
		w:  &failingWriter{},
		bw: bufio.NewWriter(&failingWriter{}),
	}
	_, err = w2.bw.WriteString("pending")
	require.NoError(t, err)
	require.NoError(t, w2.Close())

	// Failing closer with no flush error surfaces the close error.
	w3 := &AuditWriter{
		w:      new(bytes.Buffer),
		bw:     bufio.NewWriter(new(bytes.Buffer)),
		closer: covercoreFailingCloser{},
	}
	require.Error(t, w3.Close())

	// Failing closer combined with a flush error yields a combined error.
	w4 := &AuditWriter{
		w:      &failingWriter{},
		bw:     bufio.NewWriter(&failingWriter{}),
		closer: covercoreFailingCloser{},
	}
	_, err = w4.bw.WriteString("pending")
	require.NoError(t, err)
	err = w4.Close()
	require.Error(t, err)
	require.Contains(t, err.Error(), "close")
}

type covercoreFailingCloser struct{}

func (covercoreFailingCloser) Close() error {
	return errors.New("closer always fails")
}

// TestCovercore_AppendPreflightEventFields verifies the preflight event
// carries the report fields through.
func TestCovercore_AppendPreflightEventFields(t *testing.T) {
	buf := new(bytes.Buffer)
	w := NewAuditWriterFrom(buf, false, true)
	report := ScanReport{
		ScanID:    "scan-9",
		ToolName:  "workspace_exec",
		Backend:   BackendWorkspaceExec,
		Decision:  DecisionDeny,
		RiskLevel: RiskCritical,
		Findings: []Finding{
			{RuleID: "b"},
			{RuleID: "a"},
			{RuleID: "a"}, // duplicate is dropped
		},
		Redacted:    true,
		Intercepted: true,
	}
	require.NoError(t, w.appendPreflight(report))

	var ev AuditEvent
	require.NoError(t, json.Unmarshal(
		[]byte(strings.TrimSpace(buf.String())), &ev))
	require.Equal(t, AuditPhasePreflight, ev.Phase)
	require.Equal(t, "scan-9", ev.ScanID)
	require.Equal(t, []string{"b", "a"}, ev.RuleIDs)
	require.True(t, ev.Redacted)
	require.True(t, ev.Intercepted)
}

// TestCovercore_AppendPostExecuteEventFields verifies the post_execute
// event carries sizes, truncation, and execution state.
func TestCovercore_AppendPostExecuteEventFields(t *testing.T) {
	buf := new(bytes.Buffer)
	w := NewAuditWriterFrom(buf, false, true)
	require.NoError(t, w.appendPostExecute(ScanEvent{
		ScanID:    "scan-10",
		ToolName:  "exec_command",
		Backend:   BackendHostExec,
		Decision:  DecisionAllow,
		RiskLevel: RiskLow,
		RuleIDs:   []string{"r1"},
	}, 1234, true, "error"))

	var ev AuditEvent
	require.NoError(t, json.Unmarshal(
		[]byte(strings.TrimSpace(buf.String())), &ev))
	require.Equal(t, AuditPhasePostExecute, ev.Phase)
	require.Equal(t, "scan-10", ev.ScanID)
	require.Equal(t, int64(1234), ev.OutputBytes)
	require.True(t, ev.Truncated)
	require.Equal(t, "error", ev.Execution)
}

// TestCovercore_RuleIDsFromFindingsDedup covers the nil and dedup paths.
func TestCovercore_RuleIDsFromFindingsDedup(t *testing.T) {
	require.Nil(t, ruleIDsFromFindings(nil))
	require.Equal(t, []string{"a", "b"}, ruleIDsFromFindings([]Finding{
		{RuleID: "a"}, {RuleID: "b"}, {RuleID: "a"},
	}))
}

// TestCovercore_ScanEventContextRoundTrip covers the context stash and
// lookup helpers.
func TestCovercore_ScanEventContextRoundTrip(t *testing.T) {
	// Nil context is upgraded to Background.
	ctx := withScanEvent(nil, ScanEvent{ScanID: "ctx-1"})
	require.NotNil(t, ctx)
	ev, ok := scanEventFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "ctx-1", ev.ScanID)

	// Missing key reports ok=false.
	_, ok = scanEventFromContext(context.Background())
	require.False(t, ok)

	// Nil context reports ok=false.
	_, ok = scanEventFromContext(nil)
	require.False(t, ok)
}

// TestCovercore_AuditAppendOversizedEvent covers the buffered-write error
// path: an event larger than the bufio buffer is written straight to the
// underlying writer, surfacing its error.
func TestCovercore_AuditAppendOversizedEvent(t *testing.T) {
	big := strings.Repeat("x", 8192)

	w := NewAuditWriterFrom(&failingWriter{}, true, true)
	err := w.Append(AuditEvent{ScanID: "s", ToolName: big})
	require.Error(t, err)
	require.Contains(t, err.Error(), "write audit event")

	// A non-required writer swallows the same failure.
	w2 := NewAuditWriterFrom(&failingWriter{}, false, true)
	require.NoError(t, w2.Append(AuditEvent{ScanID: "s", ToolName: big}))
}
