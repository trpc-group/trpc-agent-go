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

func TestCommandPolicyCannotBeBypassedWithExecutablePath(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		command string
		match   bool
	}{
		{name: "basename denies absolute path", entries: []string{"dd"}, command: "/usr/bin/dd", match: true},
		{name: "basename allows absolute path", entries: []string{"go"}, command: "/usr/local/bin/go", match: true},
		{name: "Windows executable suffix", entries: []string{"go"}, command: `C:\\Tools\\go.exe`, match: true},
		{name: "path-specific entry does not allow bare basename", entries: []string{"/trusted/tools/go"}, command: "go", match: false},
		{name: "path-specific exact match", entries: []string{"/trusted/tools/go"}, command: "/trusted/tools/go", match: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := commandMatches(test.entries, test.command); got != test.match {
				t.Fatalf("commandMatches(%v, %q) = %v, want %v",
					test.entries, test.command, got, test.match)
			}
		})
	}

	policy := testPolicy()
	policy.Commands.Allowed = append(policy.Commands.Allowed, "dd")
	policy.Commands.Denied = []string{"dd"}
	guard, err := New(policy)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	report, err := guard.Scan(
		context.Background(),
		commandRequest(BackendWorkspace, "/usr/bin/dd if=/dev/zero of=workspace.img"),
	)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if report.Decision != tool.PermissionActionDeny || !hasRule(report, "command.denied") {
		t.Fatalf("absolute denied executable bypassed policy: %+v", report)
	}

	report, err = guard.Scan(
		context.Background(),
		commandRequest(BackendWorkspace, "/usr/bin/go test ./tool/safety"),
	)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if report.Decision != tool.PermissionActionAllow {
		t.Fatalf("allowed executable path decision = %s, matches=%+v",
			report.Decision, report.Matches)
	}
}
