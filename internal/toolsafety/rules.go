// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

// Package toolsafety provides a Tool Execution Safety Guard that scans
// commands and scripts for security risks before execution.
package toolsafety

// RuleID is the unique identifier for a safety check rule.
type RuleID string

const (
	// RuleDangerousCommand matches known dangerous command names.
	RuleDangerousCommand RuleID = "DANGEROUS_COMMAND"
	// RuleDestructivePath matches commands that destroy files or devices.
	RuleDestructivePath RuleID = "DESTRUCTIVE_PATH"
	// RuleSensitivePath matches access to sensitive file paths.
	RuleSensitivePath RuleID = "SENSITIVE_PATH"

	// RuleNetworkUnauthorized matches network access to non-whitelisted domains.
	RuleNetworkUnauthorized RuleID = "NETWORK_UNAUTHORIZED"
	// RuleNetworkAuthorized matches network access to whitelisted domains.
	RuleNetworkAuthorized RuleID = "NETWORK_AUTHORIZED"

	// RuleShellBypass matches shell wrapper commands that bypass policy.
	RuleShellBypass RuleID = "SHELL_BYPASS"
	// RuleShellWrapper matches commands that wrap another command.
	RuleShellWrapper RuleID = "SHELL_WRAPPER"
	// RuleCommandInjection matches command injection patterns.
	RuleCommandInjection RuleID = "COMMAND_INJECTION"

	// RuleHostExecPTY matches PTY session risks on hostexec.
	RuleHostExecPTY RuleID = "HOSTEXEC_PTY_SESSION"
	// RuleBackgroundProcess matches background process risks.
	RuleBackgroundProcess RuleID = "BACKGROUND_PROCESS"
	// RulePrivilegeEscalation matches privilege escalation patterns.
	RulePrivilegeEscalation RuleID = "PRIVILEGE_ESCALATION"

	// RuleDependencyInstall matches commands that install dependencies.
	RuleDependencyInstall RuleID = "DEPENDENCY_INSTALL"

	// RuleResourceTimeout matches commands with excessive timeout.
	RuleResourceTimeout RuleID = "RESOURCE_TIMEOUT"
	// RuleResourceOutputSize matches commands with excessive output.
	RuleResourceOutputSize RuleID = "RESOURCE_OUTPUT_SIZE"
	// RuleResourceSleepLoop matches commands with long sleep or loops.
	RuleResourceSleepLoop RuleID = "RESOURCE_SLEEP_LOOP"

	// RuleSensitiveLeak matches sensitive information in output.
	RuleSensitiveLeak RuleID = "SENSITIVE_LEAK"
)
