//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestDeniedPathMatchingUsesPathBoundaries(t *testing.T) {
	tests := []struct {
		text    string
		denied  string
		matches bool
	}{
		{text: "cat /etc/passwd", denied: "/etc", matches: true},
		{text: "cat /etc", denied: "/etc", matches: true},
		{text: "cat /etcetera/readme", denied: "/etc", matches: false},
		{text: "cat /tmp/etc/readme", denied: "/etc", matches: false},
		{text: "echo $(cat .env)", denied: ".env", matches: true},
		{text: "cat .environment", denied: ".env", matches: false},
		{text: "cat credentials.json", denied: "credentials.json", matches: true},
		{text: "cat credentials.json.bak", denied: "credentials.json", matches: false},
		{text: "cat ~/.ssh/id_ed25519", denied: "~/.ssh", matches: true},
	}
	for _, test := range tests {
		if got := containsPathReference(test.text, test.denied); got != test.matches {
			t.Errorf("containsPathReference(%q, %q) = %v, want %v",
				test.text, test.denied, got, test.matches)
		}
	}

	policy := testPolicy()
	guard, err := New(policy)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	report, err := guard.Scan(
		context.Background(),
		commandRequest(BackendWorkspace, "cat /etcetera/readme"),
	)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if report.Decision != tool.PermissionActionAllow {
		t.Fatalf("lookalike path decision = %s, matches=%+v",
			report.Decision, report.Matches)
	}
}
func TestLexicalPathNormalizationBlocksTraversalAndWindowsPaths(t *testing.T) {
	tests := []struct {
		text    string
		denied  string
		matches bool
	}{
		{text: "cat /tmp/../etc/passwd", denied: "/etc", matches: true},
		{text: "cat .aws/./credentials", denied: ".aws/credentials", matches: true},
		{text: "cat C:\\Users\\runner\\.aws\\.\\credentials", denied: ".aws/credentials", matches: true},
		{text: "cat C:\\Users\\runner\\.ssh\\id_ed25519", denied: "/.ssh/", matches: true},
		{text: "cat /tmp/etc/readme", denied: "/etc", matches: false},
		{text: "cat .awsx/credentials", denied: ".aws/credentials", matches: false},
	}
	for _, test := range tests {
		if got := containsPathReference(test.text, test.denied); got != test.matches {
			t.Errorf("containsPathReference(%q, %q) = %v, want %v",
				test.text, test.denied, got, test.matches)
		}
	}

	for _, candidate := range []string{
		"/tmp/../etc/passwd",
		"C:\\Temp\\..\\Windows\\System32\\drivers\\etc\\hosts",
	} {
		if !isSystemPath(candidate) {
			t.Errorf("isSystemPath(%q) = false, want true", candidate)
		}
	}
	if marker := credentialReference("cat C:\\Users\\runner\\.aws\\.\\credentials"); marker == "" {
		t.Fatal("credentialReference() did not detect normalized Windows credential path")
	}
}

func TestWorkspaceWorkingDirectoryCannotEscape(t *testing.T) {
	guard, err := New(testPolicy())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, test := range []struct {
		name    string
		backend Backend
		cwd     string
		want    tool.PermissionAction
	}{
		{name: "workspace traversal", backend: BackendWorkspace,
			cwd: "../../outside", want: tool.PermissionActionDeny},
		{name: "workspace absolute", backend: BackendWorkspace,
			cwd: "/tmp/build", want: tool.PermissionActionDeny},
		{name: "skill Windows absolute", backend: BackendSkill,
			cwd: "C:\\temp\\build", want: tool.PermissionActionDeny},
		{name: "workspace relative", backend: BackendWorkspace,
			cwd: "pkg/review", want: tool.PermissionActionAllow},
		{name: "host absolute remains policy scoped", backend: BackendHost,
			cwd: "/tmp/build", want: tool.PermissionActionAsk},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := commandRequest(test.backend, "go test ./tool/safety")
			request.CWD = test.cwd
			if test.backend == BackendHost {
				request.YieldMS = intPointer(0)
			}
			if test.backend == BackendSkill {
				request.ToolName = "skill_exec"
				request.Skill = "code-review"
			}
			report, scanErr := guard.Scan(context.Background(), request)
			if scanErr != nil {
				t.Fatalf("Scan() error = %v", scanErr)
			}
			if report.Decision != test.want {
				t.Fatalf("decision = %s, want %s; matches=%+v",
					report.Decision, test.want, report.Matches)
			}
		})
	}
}
