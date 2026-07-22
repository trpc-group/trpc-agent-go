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

func TestContainsActiveShellExpansionHonorsQuotingAndEscapes(t *testing.T) {
	tests := []struct {
		command string
		active  bool
	}{
		{command: "echo $HOME", active: true},
		{command: "echo ${HOME}", active: true},
		{command: "echo $(whoami)", active: true},
		{command: "echo `whoami`", active: true},
		{command: `echo "$HOME"`, active: true},
		{command: "echo $?", active: true},
		{command: "echo '$HOME'", active: false},
		{command: "echo '$(whoami)'", active: false},
		{command: "echo '`whoami`'", active: false},
		{command: `echo \$HOME`, active: false},
		{command: "echo ordinary", active: false},
	}
	for _, test := range tests {
		if got := containsActiveShellExpansion(test.command); got != test.active {
			t.Errorf("containsActiveShellExpansion(%q) = %v, want %v",
				test.command, got, test.active)
		}
	}
}

func TestQuotedShellMetacharactersDoNotCreateFalsePositive(t *testing.T) {
	guard, err := New(testPolicy())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, command := range []string{
		"echo '$HOME'",
		"echo '$(not-executed)'",
		"echo '`not-executed`'",
		`echo \$HOME`,
	} {
		report, scanErr := guard.Scan(
			context.Background(),
			commandRequest(BackendWorkspace, command),
		)
		if scanErr != nil {
			t.Fatalf("Scan(%q) error = %v", command, scanErr)
		}
		if report.Decision != tool.PermissionActionAllow {
			t.Errorf("Scan(%q) decision = %s, matches=%+v",
				command, report.Decision, report.Matches)
		}
	}
}
func TestContainsActiveWindowsExpansion(t *testing.T) {
	tests := []struct {
		command string
		active  bool
	}{
		{command: "echo %PATH%", active: true},
		{command: "echo \"%PATH%\"", active: true},
		{command: "echo '%PATH%'", active: true},
		{command: "echo %PATH:~0,1%", active: true},
		{command: "echo %PATH:C:=D:%", active: true},
		{command: "echo !PATH!", active: true},
		{command: "echo 100%", active: false},
		{command: "echo %%PATH%%", active: false},
		{command: "echo ^%PATH^%", active: false},
	}
	for _, test := range tests {
		if got := containsActiveWindowsExpansion(test.command); got != test.active {
			t.Errorf("containsActiveWindowsExpansion(%q) = %v, want %v",
				test.command, got, test.active)
		}
	}
}
