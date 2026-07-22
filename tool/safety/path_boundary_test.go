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
