//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "testing"

func TestDestructiveDeleteVariants(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    bool
	}{
		{name: "rm recursive long", command: "rm", args: []string{"--recursive", "work"}, want: true},
		{name: "rm force long case folded", command: "rm", args: []string{"--FORCE", "file"}, want: true},
		{name: "rm combined short", command: "rm", args: []string{"-fr", "work"}, want: true},
		{name: "rm benign long option", command: "rm", args: []string{"--preserve-root", "file"}},
		{name: "rmdir slash s", command: "rmdir", args: []string{"/s", "work"}, want: true},
		{name: "del slash q", command: "del", args: []string{"/q", "file"}, want: true},
		{name: "erase recursive short", command: "erase", args: []string{"-r", "work"}, want: true},
		{name: "rmdir recursive long", command: "rmdir", args: []string{"--recursive", "work"}, want: true},
		{name: "rmdir safe", command: "rmdir", args: []string{"empty-dir"}},
		{name: "powershell recurse", command: "remove-item", args: []string{"-Recurse", "work"}, want: true},
		{name: "powershell force", command: "remove-item", args: []string{"-Force", "file"}, want: true},
		{name: "powershell safe", command: "remove-item", args: []string{"file"}},
		{name: "git no args", command: "git"},
		{name: "git clean dry run", command: "git", args: []string{"clean", "-n"}, want: false},
		{name: "git clean combined dry run", command: "git", args: []string{"clean", "-nfdx"}, want: false},
		{name: "git clean long dry run wins", command: "git", args: []string{"clean", "--force", "--dry-run"}},
		{name: "git clean force", command: "git", args: []string{"clean", "--force"}, want: true},
		{name: "git clean combined force", command: "git", args: []string{"clean", "-fdx"}, want: true},
		{name: "git clean without force", command: "git", args: []string{"clean", "-d"}},
		{name: "git reset hard", command: "git", args: []string{"reset", "--HARD"}, want: true},
		{name: "git reset soft", command: "git", args: []string{"reset", "--soft"}},
		{name: "git status", command: "git", args: []string{"status"}},
		{name: "find delete", command: "find", args: []string{".", "-delete"}, want: true},
		{name: "find print", command: "find", args: []string{".", "-print"}},
		{name: "unrelated command", command: "echo", args: []string{"-rf"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isDestructiveDelete(test.command, test.args); got != test.want {
				t.Fatalf("isDestructiveDelete(%q, %v) = %v, want %v",
					test.command, test.args, got, test.want)
			}
		})
	}
}
func TestGitGlobalOptionsDoNotHideDestructiveSubcommands(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{args: []string{"-C", "repo", "clean", "-fdx"}, want: true},
		{args: []string{"--no-pager", "reset", "--hard"}, want: true},
		{args: []string{"-C", "repo", "clean", "-nfdx"}, want: false},
		{args: []string{"-C", "repo", "status"}, want: false},
	}
	for _, test := range tests {
		if got := isDestructiveGit(test.args); got != test.want {
			t.Errorf("isDestructiveGit(%v) = %v, want %v", test.args, got, test.want)
		}
	}
	for _, args := range [][]string{
		{"-c", "core.pager=sh", "status"},
		{"-ccore.sshCommand=nc", "status"},
		{"--config-env=core.sshCommand=ENV", "status"},
		{"--exec-path=/tmp/helpers", "status"},
	} {
		if !hasUnsafeGitExecutionOption(args) {
			t.Errorf("hasUnsafeGitExecutionOption(%v) = false, want true", args)
		}
	}
	if hasUnsafeGitExecutionOption([]string{"-C", "repo", "status"}) {
		t.Fatal("git -C was mistaken for lowercase -c execution configuration")
	}
}

func TestDependencyChangesWithLeadingGlobalOptions(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "npm", args: []string{"--global", "install", "left-pad"}, want: true},
		{name: "pip", args: []string{"--proxy=https://proxy.golang.org", "install", "requests"}, want: true},
		{name: "go", args: []string{"-C", "tools", "install", "./cmd/tool"}, want: true},
		{name: "npm", args: []string{"--global", "test"}, want: false},
	}
	for _, test := range tests {
		if got := isDependencyChange(test.name, test.args); got != test.want {
			t.Errorf("isDependencyChange(%q, %v) = %v, want %v",
				test.name, test.args, got, test.want)
		}
	}
}

func TestSleepDurationsUseUnitsAndPowerShellParameters(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    bool
	}{
		{name: "seconds below limit", command: "sleep", args: []string{"5s"}, want: false},
		{name: "minutes", command: "sleep", args: []string{"1m"}, want: true},
		{name: "hours", command: "sleep", args: []string{"1h"}, want: true},
		{name: "days", command: "sleep", args: []string{"1d"}, want: true},
		{name: "infinity", command: "sleep", args: []string{"infinity"}, want: true},
		{name: "PowerShell seconds", command: "start-sleep", args: []string{"-Seconds", "60"}, want: true},
		{name: "PowerShell milliseconds", command: "start-sleep", args: []string{"-Milliseconds", "500"}, want: false},
	}
	for _, test := range tests {
		if got := sleepCommandExceeds(test.command, test.args, 30); got != test.want {
			t.Errorf("sleepCommandExceeds(%q, %v) = %v, want %v",
				test.command, test.args, got, test.want)
		}
	}
}
