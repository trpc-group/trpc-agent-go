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
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHelperFunctions(t *testing.T) {
	if bumpRisk(RiskLow) != RiskMedium || bumpRisk(RiskMedium) != RiskHigh ||
		bumpRisk(RiskHigh) != RiskCritical || bumpRisk(RiskCritical) != RiskCritical ||
		bumpRisk(RiskNone) != RiskNone {
		t.Error("bumpRisk mapping wrong")
	}
	if hasDeny([]Finding{{Decision: DecisionAsk}}) || !hasDeny([]Finding{{Decision: DecisionDeny}}) {
		t.Error("hasDeny wrong")
	}
	if !argvHasFlagLetter([]string{"rm", "-rf"}, 'f') || argvHasFlagLetter([]string{"rm", "--force"}, 'x') {
		t.Error("argvHasFlagLetter wrong")
	}
	if !argvContainsPrefix([]string{"dd", "of=/dev/sda"}, "of=") || argvContainsPrefix([]string{"x"}, "z") {
		t.Error("argvContainsPrefix wrong")
	}
	if !argsContainAll([]string{"install", "x"}, []string{"install"}) ||
		argsContainAll([]string{"a"}, []string{"b"}) || argsContainAll([]string{"a"}, nil) {
		t.Error("argsContainAll wrong")
	}
	if !hasRecursiveForce([]string{"rm", "--recursive", "--force"}) ||
		hasRecursiveForce([]string{"rm", "-r"}) || hasRecursiveForce([]string{"rm", "-"}) {
		t.Error("hasRecursiveForce wrong")
	}
	if firstNonEmptyStr("", "", "x") != "x" || firstNonEmptyStr() != "" {
		t.Error("firstNonEmptyStr wrong")
	}
	if firstTimeout((*int)(nil), (*int)(nil), 42) != 42 {
		t.Error("firstTimeout int case wrong")
	}
	if v := 7; firstTimeout(&v) != 7 {
		t.Error("firstTimeout *int case wrong")
	}
	if decisionRank(DecisionNeedsHumanReview) <= decisionRank(DecisionAsk) {
		t.Error("needs_human_review should outrank ask")
	}
}

func TestParseDurationSeconds(t *testing.T) {
	cases := []struct {
		in  string
		sec float64
		ok  bool
	}{
		{"120", 120, true}, {"5m", 300, true}, {"2h", 7200, true}, {"1d", 86400, true},
		{"1.5", 1.5, true}, {"inf", 1e18, true}, {"infinity", 1e18, true},
		{"", 0, false}, {"abc", 0, false}, {"m", 0, false},
	}
	for _, c := range cases {
		got, ok := parseDurationSeconds(c.in)
		if ok != c.ok || (ok && got != c.sec) {
			t.Errorf("parseDurationSeconds(%q)=%v,%v want %v,%v", c.in, got, ok, c.sec, c.ok)
		}
	}
}

func TestRiskForAndPolicyAccessor(t *testing.T) {
	p := DefaultPolicy()
	p.RiskOverrides = map[string]RiskLevel{"x.y": RiskCritical}
	if err := p.compile(); err != nil {
		t.Fatalf("compile: %v", err)
	}
	if p.riskFor("x.y", RiskLow) != RiskCritical {
		t.Error("override not applied")
	}
	if p.riskFor("z", RiskMedium) != RiskMedium {
		t.Error("fallback wrong")
	}
	// NewScanner compiles a private snapshot, so Policy() is a copy, not p; it
	// must still preserve the configured override.
	if sc := NewScanner(p); sc.Policy().riskFor("x.y", RiskLow) != RiskCritical {
		t.Error("Policy() snapshot lost the risk override")
	}
}

func TestDecodeCodeBlocks(t *testing.T) {
	b, err := decodeCodeBlocks(json.RawMessage(`[{"language":"python","code":"x"}]`))
	if err != nil || len(b) != 1 || b[0].Language != "python" {
		t.Fatalf("array: %v %+v", err, b)
	}
	b, err = decodeCodeBlocks(json.RawMessage(`{"language":"bash","code":"ls"}`))
	if err != nil || len(b) != 1 || b[0].Code != "ls" {
		t.Fatalf("object: %v %+v", err, b)
	}
	b, err = decodeCodeBlocks(json.RawMessage(`"[{\"language\":\"sh\",\"code\":\"pwd\"}]"`))
	if err != nil || len(b) != 1 || b[0].Language != "sh" {
		t.Fatalf("double-encoded string: %v %+v", err, b)
	}
	if b, err := decodeCodeBlocks(nil); err != nil || b != nil {
		t.Fatalf("empty: %v %+v", err, b)
	}
	if _, err := decodeCodeBlocks(json.RawMessage(`123`)); err == nil {
		t.Fatal("expected error for a bare number")
	}
	if _, err := decodeCodeBlocks(json.RawMessage(`{`)); err == nil {
		t.Fatal("expected error for malformed json")
	}
}

func TestAuditWithClock(t *testing.T) {
	var buf bytes.Buffer
	fixed := time.Date(2026, 7, 2, 3, 0, 0, 0, time.UTC)
	w := NewAuditWriter(&buf, WithAuditClock(func() time.Time { return fixed }))
	if err := w.Record(sampleReport()); err != nil {
		t.Fatalf("record: %v", err)
	}
	var rec AuditRecord
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.Time != "2026-07-02T03:00:00Z" {
		t.Errorf("time=%q want RFC3339 UTC", rec.Time)
	}
}

func TestLoadPolicyErrorPaths(t *testing.T) {
	dir := t.TempDir()
	badExt := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(badExt, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(badExt); err == nil {
		t.Error("unsupported extension should error")
	}
	badJSON := filepath.Join(dir, "p.json")
	if err := os.WriteFile(badJSON, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(badJSON); err == nil {
		t.Error("malformed json should error")
	}
	if _, err := LoadPolicy(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Error("missing file should error")
	}
}

func TestPolicyEnumValidation(t *testing.T) {
	p := DefaultPolicy()
	p.DependencyInstall.Decision = "block"
	if err := p.compile(); err == nil {
		t.Error("bad dependency decision should error")
	}
	p = DefaultPolicy()
	p.RiskOverrides = map[string]RiskLevel{"r": "boom"}
	if err := p.compile(); err == nil {
		t.Error("bad risk override should error")
	}
}

// TestExtraScanCoverage exercises rule branches not hit by the sample set.
func TestExtraScanCoverage(t *testing.T) {
	sc := NewScanner(nil)
	cases := []struct {
		backend Backend
		cmd     string
		want    Decision
	}{
		{BackendWorkspaceExec, ":(){ :|:& };:", DecisionDeny}, // fork bomb (line regex)
		{BackendWorkspaceExec, "shred -u /data/file", DecisionDeny},
		{BackendWorkspaceExec, "mkfs.ext4 /dev/sdb", DecisionDeny},
		{BackendHostExec, "su root", DecisionDeny},
		{BackendHostExec, "doas reboot", DecisionDeny},
		{BackendWorkspaceExec, "screen -S x", DecisionAsk},
		{BackendWorkspaceExec, "watch ls", DecisionAsk},
		{BackendWorkspaceExec, "perl -e print", DecisionDeny},
		{BackendWorkspaceExec, "curl -sS", DecisionAsk}, // network, target undetermined
		{BackendWorkspaceExec, "bash -i", DecisionDeny}, // reverse shell / interpreter
	}
	for _, c := range cases {
		r := sc.Scan(context.Background(), ScanInput{ToolName: "t", Backend: c.backend, Command: c.cmd})
		if r.Decision != c.want {
			t.Errorf("%q: decision=%s want %s; findings=%+v", c.cmd, r.Decision, c.want, r.Findings)
		}
	}
}

func TestScanCodeBlocksVariants(t *testing.T) {
	sc := NewScanner(nil)
	// A bash code block reuses the command scanner.
	r := sc.Scan(context.Background(), ScanInput{
		ToolName: "execute_code", Backend: BackendCodeExec,
		CodeBlocks: []CodeBlock{{Language: "bash", Code: "rm -rf /"}},
	})
	if r.Decision != DecisionDeny {
		t.Errorf("bash block: decision=%s findings=%+v", r.Decision, r.Findings)
	}
	// A foreign block referencing a secret path via a loose token.
	r = sc.Scan(context.Background(), ScanInput{
		ToolName: "execute_code", Backend: BackendCodeExec,
		CodeBlocks: []CodeBlock{{Language: "python", Code: "open('/root/.ssh/id_rsa')"}},
	})
	if r.Decision != DecisionDeny {
		t.Errorf("python secret path: decision=%s findings=%+v", r.Decision, r.Findings)
	}
}
