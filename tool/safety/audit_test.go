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
	"strings"
	"testing"
)

func sampleReport() ScanReport {
	r := ScanReport{
		ToolName: "exec_command",
		Backend:  BackendHostExec,
		Findings: []Finding{{RuleID: "cmd.dangerous_delete", Decision: DecisionDeny, RiskLevel: RiskCritical}},
	}
	r.aggregate()
	return r
}

func TestAuditWriterAppendsJSONL(t *testing.T) {
	var buf bytes.Buffer
	w := NewAuditWriter(&buf, WithoutTimestamp())
	if err := w.Record(sampleReport()); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := w.Record(sampleReport()); err != nil {
		t.Fatalf("record: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), buf.String())
	}
	var rec AuditRecord
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unmarshal audit line: %v", err)
	}
	if rec.Decision != DecisionDeny || rec.RuleID != "cmd.dangerous_delete" || !rec.Blocked {
		t.Errorf("unexpected audit record: %+v", rec)
	}
	if rec.Time != "" {
		t.Errorf("WithoutTimestamp should omit time, got %q", rec.Time)
	}
}

func TestAuditNilWriterNoOp(t *testing.T) {
	var w *AuditWriter
	if err := w.Record(sampleReport()); err != nil {
		t.Errorf("nil writer should be a no-op, got %v", err)
	}
}
