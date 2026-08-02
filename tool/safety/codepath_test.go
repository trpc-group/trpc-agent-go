//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//

package safety

import (
	"context"
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// codeArgs builds an execute_code payload for one block.
func codeArgs(lang, code string) []byte {
	b, err := json.Marshal(map[string]any{
		"code_blocks": []codeBlock{{Language: lang, Code: code}},
	})
	if err != nil {
		panic(err)
	}
	return b
}

// TestCodePathFindings covers the forbidden-path pass over non-shell code. The
// argv rules never see a python/js block, so without it a credential read in
// code is allowed while the identical shell command is denied.
func TestCodePathFindings(t *testing.T) {
	p := loadExamplePolicy(t)
	tests := []struct {
		name    string
		lang    string
		code    string
		wantHit bool
	}{
		{"python open ssh key", "python", `open('/root/.ssh/id_rsa').read()`, true},
		{"python double quotes", "python", `f = open("/home/u/.ssh/id_rsa")`, true},
		{"python relative dotenv", "python", `open("config/.env")`, true},
		{"python bare dotenv", "python", `open(".env")`, true},
		{"python tilde key", "python", `open("~/.ssh/id_rsa")`, true},
		{"python file uri", "python", `urlopen("file:///home/u/.ssh/id_rsa")`, true},
		{"js backtick literal", "javascript", "fs.readFileSync(`/home/u/.ssh/id_rsa`)", true},
		{"js single quotes", "javascript", `fs.readFileSync('/home/u/.ssh/id_rsa')`, true},
		{"windows escaped separators", "python", `open("C:\\Users\\me\\.ssh\\id_rsa")`, true},
		{"powershell block", "powershell", `Get-Content "/home/u/.ssh/id_rsa"`, true},

		{"benign message", "python", `print("hello world")`, false},
		{"bare word is not a path", "python", `print("credentials")`, false},
		{"identifier mentioning env", "python", `env = os.environ.get("HOME")`, false},
		{"unrelated path", "python", `open("/tmp/report.txt")`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.codePathFindings(tt.code)
			if hit := len(got) > 0; hit != tt.wantHit {
				t.Fatalf("codePathFindings(%q) hit = %v, want %v (findings %+v)",
					tt.code, hit, tt.wantHit, got)
			}
			if !tt.wantHit {
				return
			}
			if got[0].RuleID != ruleCredID {
				t.Errorf("rule id = %q, want %q", got[0].RuleID, ruleCredID)
			}
			if got[0].RiskLevel != RiskCritical {
				t.Errorf("risk = %q, want %q", got[0].RiskLevel, RiskCritical)
			}
			if got[0].Recommendation == "" {
				t.Errorf("finding carries no recommendation")
			}
		})
	}
}

// TestCodeCredentialReadIsDenied is the end-to-end acceptance check: a
// credential read inside an execute_code block must be denied, matching the
// shell-side guarantee (criterion 3: 100% detection for key reads).
func TestCodeCredentialReadIsDenied(t *testing.T) {
	var last Report
	g, err := NewGuard(WithPolicy(loadExamplePolicy(t)),
		WithReportSink(func(r Report) { last = r }))
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	dec, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "execute_code",
		Arguments: codeArgs("python", "print(open('/root/.ssh/id_rsa').read())"),
	})
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("action = %v, want deny", dec.Action)
	}
	if !hasRule(last.Findings, ruleCredID) {
		t.Errorf("missing %s in %+v", ruleCredID, last.Findings)
	}
}

// TestCodePathDeduplicates ensures a literal repeated across a block yields one
// finding, not one per occurrence.
func TestCodePathDeduplicates(t *testing.T) {
	p := loadExamplePolicy(t)
	code := `open("/root/.ssh/id_rsa"); open("/root/.ssh/id_rsa")`
	if got := p.codePathFindings(code); len(got) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(got), got)
	}
}

// TestUnescapeCodeLiteral covers the escape handling that normalizes a literal
// before it is matched against the forbidden-path globs.
func TestUnescapeCodeLiteral(t *testing.T) {
	tests := []struct{ in, want string }{
		{`plain`, `plain`},
		{`C:\\Users\\me`, `C:\Users\me`},
		{`say \"hi\"`, `say "hi"`},
		{`it\'s`, `it's`},
		{`line\nbreak`, `line\nbreak`}, // unrelated escapes are left alone
		{`trailing\`, `trailing\`},
	}
	for _, tt := range tests {
		if got := unescapeCodeLiteral(tt.in); got != tt.want {
			t.Errorf("unescapeCodeLiteral(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestPathLikeLiteral documents which literals are treated as paths.
func TestPathLikeLiteral(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"credentials", false},
		{"id_rsa", false},
		{"/etc/shadow", true},
		{"a/b", true},
		{`C:\x`, true},
		{"~/.ssh", true},
		{".env", true},
		{"./key", true},
	}
	for _, tt := range tests {
		if got := pathLikeLiteral(tt.in); got != tt.want {
			t.Errorf("pathLikeLiteral(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
