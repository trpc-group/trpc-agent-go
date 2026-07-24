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
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// ---------------------------------------------------------------------------
// R-DEL-001  DangerousCommandRule
// ---------------------------------------------------------------------------

// DangerousCommandRule detects destructive commands and access to system paths.
// Rule ID: R-DEL-001.
type DangerousCommandRule struct{}

// ID returns the rule identifier "R-DEL-001".
func (r *DangerousCommandRule) ID() string { return "R-DEL-001" }

// Name returns the human-readable rule name.
func (r *DangerousCommandRule) Name() string { return "Dangerous Command" }

var dangerousCmdPatterns = []string{
	"rm -rf", "rm -fr", "rmdir ", "mkfs ", "mkfs.", "dd if=",
	"format ", "fdisk ",
}

var systemPathPatterns = []string{
	"/etc/", "/boot/", "/usr/",
	"~/.ssh", "~/.gnupg",
	".env", "credentials",
}

// Scan evaluates the input against policy-denied commands and paths.
// When no policy entries are configured it falls back to built-in patterns.
// Scan returns matching deny findings for destructive commands or sensitive paths.
func (r *DangerousCommandRule) Scan(_ context.Context, input ScanInput, policy PolicyFile) []Finding {
	text := normalizedScanText(input)
	var findings []Finding

	// Use DeniedCommands from the policy (policy-driven).
	deniedCmds := policy.DeniedCommands
	if len(deniedCmds) == 0 {
		deniedCmds = dangerousCmdPatterns
	}
	for _, pat := range deniedCmds {
		if strings.Contains(text, pat) {
			findings = append(findings, Finding{
				RuleID:         r.ID(),
				RuleName:       r.Name(),
				RiskLevel:      RiskLevelCritical,
				Decision:       DecisionDeny,
				Evidence:       pat,
				Recommendation: "Remove or restrict the destructive command; use safer alternatives.",
			})
			return findings
		}
	}

	// Use DeniedPaths from the policy (policy-driven).
	deniedPaths := policy.DeniedPaths
	if len(deniedPaths) == 0 {
		deniedPaths = systemPathPatterns
	}
	normalized := normalizePaths(text)
	for _, pat := range deniedPaths {
		if strings.Contains(normalized, pat) {
			findings = append(findings, Finding{
				RuleID:         r.ID(),
				RuleName:       r.Name(),
				RiskLevel:      RiskLevelCritical,
				Decision:       DecisionDeny,
				Evidence:       pat,
				Recommendation: "Avoid accessing system or sensitive paths directly.",
			})
			return findings
		}
	}

	return findings
}

// ---------------------------------------------------------------------------
// R-CRED-001  CredentialAccessRule
// ---------------------------------------------------------------------------

// CredentialAccessRule detects attempts to read credential files.
// Rule ID: R-CRED-001.
type CredentialAccessRule struct{}

// ID returns the rule identifier "R-CRED-001".
func (r *CredentialAccessRule) ID() string { return "R-CRED-001" }

// Name returns the human-readable rule name.
func (r *CredentialAccessRule) Name() string { return "Credential Access" }

var credPathPatterns = []string{
	"~/.ssh/",
	"~/.aws/credentials",
	"~/.gnupg/",
	".env",
	".credentials",
	"~/.kube/config",
	"/etc/shadow",
	"/etc/ssh/",
}

// Scan checks the input text for credential file path patterns.
func (r *CredentialAccessRule) Scan(_ context.Context, input ScanInput, _ PolicyFile) []Finding {
	text := normalizePaths(normalizedScanText(input))
	var findings []Finding

	for _, pat := range credPathPatterns {
		if strings.Contains(text, pat) {
			findings = append(findings, Finding{
				RuleID:         r.ID(),
				RuleName:       r.Name(),
				RiskLevel:      RiskLevelCritical,
				Decision:       DecisionDeny,
				Evidence:       pat,
				Recommendation: "Do not access credential or secret files directly.",
			})
			return findings
		}
	}

	return findings
}

// ---------------------------------------------------------------------------
// R-SHELL-001  ShellBypassRule
// ---------------------------------------------------------------------------

// ShellBypassRule detects unsafe shell constructs and shell wrapper commands.
// Rule ID: R-SHELL-001.
type ShellBypassRule struct{}

// ID returns the rule identifier "R-SHELL-001".
func (r *ShellBypassRule) ID() string { return "R-SHELL-001" }

// Name returns the human-readable rule name.
func (r *ShellBypassRule) Name() string { return "Shell Bypass" }

var shellWrapperCmds = []string{
	"sudo ", "su ", "doas ",
	"nohup ", "xargs ", "env ",
}

// Scan detects unsafe shell constructs via shellsafe.Parse and shell wrapper commands.
func (r *ShellBypassRule) Scan(_ context.Context, input ScanInput, _ PolicyFile) []Finding {
	text := normalizedScanText(input)
	var findings []Finding

	// Use shellsafe.Parse to detect unsafe shell constructs in the command.
	if input.Command != "" {
		_, err := shellsafe.Parse(input.Command)
		if err != nil {
			findings = append(findings, Finding{
				RuleID:         r.ID(),
				RuleName:       r.Name(),
				RiskLevel:      RiskLevelHigh,
				Decision:       DecisionDeny,
				Evidence:       err.Error(),
				Recommendation: "Remove shell metacharacters, substitutions, or redirections from the command.",
			})
			return findings
		}
	}

	// Detect shell wrapper commands.
	for _, w := range shellWrapperCmds {
		if strings.Contains(text, w) {
			findings = append(findings, Finding{
				RuleID:         r.ID(),
				RuleName:       r.Name(),
				RiskLevel:      RiskLevelHigh,
				Decision:       DecisionDeny,
				Evidence:       strings.TrimSpace(w),
				Recommendation: "Avoid shell wrapper commands that can bypass the safety policy.",
			})
			return findings
		}
	}

	return findings
}

// ---------------------------------------------------------------------------
// R-HOST-001  HostLongSessionRule
// ---------------------------------------------------------------------------

// HostLongSessionRule detects risky host-exec session configurations.
// Rule ID: R-HOST-001.
type HostLongSessionRule struct{}

// ID returns the rule identifier "R-HOST-001".
func (r *HostLongSessionRule) ID() string { return "R-HOST-001" }

// Name returns the human-readable rule name.
func (r *HostLongSessionRule) Name() string { return "Host Long Session" }

var privilegeEscalationCmds = []string{"sudo ", "su ", "doas "}
var processResidueCmds = []string{"nohup ", "disown ", "daemon "}

// Scan checks for privilege escalation, background PTY sessions, process residue
// commands, and excessive timeouts in host-exec backend calls.
func (r *HostLongSessionRule) Scan(_ context.Context, input ScanInput, policy PolicyFile) []Finding {
	// This rule only applies to the hostexec backend.
	if input.Backend != "hostexec" {
		return nil
	}

	text := normalizedScanText(input)
	var findings []Finding

	// Detect privilege escalation.
	for _, cmd := range privilegeEscalationCmds {
		if strings.Contains(text, cmd) {
			findings = append(findings, Finding{
				RuleID:         r.ID(),
				RuleName:       r.Name(),
				RiskLevel:      RiskLevelHigh,
				Decision:       DecisionDeny,
				Evidence:       strings.TrimSpace(cmd),
				Recommendation: "Privilege escalation is not allowed in host execution sessions.",
			})
			return findings
		}
	}

	// Detect background/PTY session indicators.
	if input.Background && input.PTY {
		findings = append(findings, Finding{
			RuleID:         r.ID(),
			RuleName:       r.Name(),
			RiskLevel:      RiskLevelHigh,
			Decision:       DecisionDeny,
			Evidence:       "PTY=true with Background=true",
			Recommendation: "Background PTY sessions are not allowed in host execution.",
		})
		return findings
	}

	// Detect process residue.
	for _, cmd := range processResidueCmds {
		if strings.Contains(text, cmd) {
			findings = append(findings, Finding{
				RuleID:         r.ID(),
				RuleName:       r.Name(),
				RiskLevel:      RiskLevelHigh,
				Decision:       DecisionDeny,
				Evidence:       strings.TrimSpace(cmd),
				Recommendation: "Process residue commands are not allowed in host execution sessions.",
			})
			return findings
		}
	}

	// Detect long timeout via the normalized ScanInput.Timeout field.
	if policy.MaxTimeoutSec > 0 && input.Timeout > 0 && input.Timeout > policy.MaxTimeoutSec {
		findings = append(findings, Finding{
			RuleID:         r.ID(),
			RuleName:       r.Name(),
			RiskLevel:      RiskLevelMedium,
			Decision:       DecisionAsk,
			Evidence:       fmt.Sprintf("timeout=%d", input.Timeout),
			Recommendation: "Reduce the session timeout or request explicit human review.",
		})
		return findings
	}

	return findings
}

// ---------------------------------------------------------------------------
// R-DEP-001  DependencyInstallRule
// ---------------------------------------------------------------------------

// DependencyInstallRule detects package installation commands.
// Rule ID: R-DEP-001.
type DependencyInstallRule struct{}

// ID returns the rule identifier "R-DEP-001".
func (r *DependencyInstallRule) ID() string { return "R-DEP-001" }

// Name returns the human-readable rule name.
func (r *DependencyInstallRule) Name() string { return "Dependency Install" }

var dependencyInstallCmds = []string{
	"go install ",
	"npm install ",
	"pip install ",
	"apt install ", "apt-get install ", "apt-get update",
	"yum install ", "yum update",
	"dnf install ",
	"brew install ",
	"cargo install ",
	"pip install --user",
}

// Scan detects package installation commands and flags them for review,
// unless the command executable is in the policy's AllowedCommands list.
func (r *DependencyInstallRule) Scan(_ context.Context, input ScanInput, policy PolicyFile) []Finding {
	text := normalizedScanText(input)

	for _, cmd := range dependencyInstallCmds {
		if !strings.Contains(text, cmd) {
			continue
		}
		// Exception: if the command executable is in AllowedCommands, skip.
		// Parse the executable from the full command to match
		// AllowListMissRule semantics.
		if isCommandAllowedExecutable(input.Command, policy.AllowedCommands) {
			return nil
		}
		return []Finding{{
			RuleID:         r.ID(),
			RuleName:       r.Name(),
			RiskLevel:      RiskLevelMedium,
			Decision:       DecisionAsk,
			Evidence:       strings.TrimSpace(cmd),
			Recommendation: "Review the dependency installation; add the command to allowed_commands if approved.",
		}}
	}

	return nil
}

// ---------------------------------------------------------------------------
// R-RES-001  ResourceAbuseRule
// ---------------------------------------------------------------------------

// ResourceAbuseRule detects resource exhaustion patterns.
// Rule ID: R-RES-001.
type ResourceAbuseRule struct{}

// ID returns the rule identifier "R-RES-001".
func (r *ResourceAbuseRule) ID() string { return "R-RES-001" }

// Name returns the human-readable rule name.
func (r *ResourceAbuseRule) Name() string { return "Resource Abuse" }

var infiniteLoopPatterns = []string{
	"while true",
	"for(;;)",
	":(){ :|:& }",
	":(){ :|:&};",
}

var largeSleepRe = regexp.MustCompile(`sleep\s+([^\s;&|]+)`)

// Scan detects fork bombs, infinite loops, large sleep values, excessive timeouts,
// and output redirection patterns.
func (r *ResourceAbuseRule) Scan(_ context.Context, input ScanInput, policy PolicyFile) []Finding {
	text := normalizedScanText(input)
	var findings []Finding

	// Detect fork bombs / infinite loops → deny.
	for _, pat := range infiniteLoopPatterns {
		if strings.Contains(text, pat) {
			findings = append(findings, Finding{
				RuleID:         r.ID(),
				RuleName:       r.Name(),
				RiskLevel:      RiskLevelHigh,
				Decision:       DecisionDeny,
				Evidence:       pat,
				Recommendation: "Infinite loops and fork bombs are not allowed.",
			})
			return findings
		}
	}

	// Detect large sleep values (>300 s).
	// Malformed or overflowing values also produce a finding (fail-closed).
	for _, m := range largeSleepRe.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			sleepValue := m[1]
			val, err := strconv.Atoi(m[1])
			if err != nil {
				// Malformed or overflowing sleep value — flag for review.
				findings = append(findings, Finding{
					RuleID:         r.ID(),
					RuleName:       r.Name(),
					RiskLevel:      RiskLevelMedium,
					Decision:       DecisionAsk,
					Evidence:       "sleep " + sleepValue + " (unparsable)",
					Recommendation: "Review the sleep value \"" + sleepValue + "\" and clarify the duration before execution.",
				})
				return findings
			}
			if val > 300 {
				findings = append(findings, Finding{
					RuleID:         r.ID(),
					RuleName:       r.Name(),
					RiskLevel:      RiskLevelMedium,
					Decision:       DecisionAsk,
					Evidence:       "sleep " + sleepValue,
					Recommendation: "Reduce the sleep duration \"" + sleepValue + "\" or request human review.",
				})
				return findings
			}
		}
	}

	// Detect timeout values exceeding policy via the normalized ScanInput.Timeout field.
	if policy.MaxTimeoutSec > 0 && input.Timeout > 0 && input.Timeout > policy.MaxTimeoutSec {
		findings = append(findings, Finding{
			RuleID:         r.ID(),
			RuleName:       r.Name(),
			RiskLevel:      RiskLevelMedium,
			Decision:       DecisionAsk,
			Evidence:       fmt.Sprintf("timeout=%d", input.Timeout),
			Recommendation: "Reduce the timeout or request human review.",
		})
		return findings
	}

	// Detect large output redirection.
	if strings.Contains(text, ">>") || strings.Contains(text, " > ") {
		findings = append(findings, Finding{
			RuleID:         r.ID(),
			RuleName:       r.Name(),
			RiskLevel:      RiskLevelMedium,
			Decision:       DecisionAsk,
			Evidence:       "output redirection detected",
			Recommendation: "Review output redirection; it may consume excessive disk space.",
		})
		return findings
	}

	return findings
}

// ---------------------------------------------------------------------------
// R-SECRET-001  SecretLeakageRule
// ---------------------------------------------------------------------------

// SecretLeakageRule detects potential secret values in the input command.
// Rule ID: R-SECRET-001.
type SecretLeakageRule struct{}

// ID returns the rule identifier "R-SECRET-001".
func (r *SecretLeakageRule) ID() string { return "R-SECRET-001" }

// Name returns the human-readable rule name.
func (r *SecretLeakageRule) Name() string { return "Secret Leakage" }

var (
	// API key prefixes commonly used by providers.
	apiKeyPrefixRe = regexp.MustCompile(`(?:sk-|key-|api-key-)[A-Za-z0-9_-]{8,}`)
	// AWS access key IDs.
	awsKeyRe = regexp.MustCompile(`AKIA[A-Z0-9]{16}`)
	// PEM private key headers.
	privateKeyRe = regexp.MustCompile(`-----BEGIN[^\n]*PRIVATE KEY-----`)
	// Bearer / Authorization headers.
	bearerTokenRe = regexp.MustCompile(`(?:Bearer\s+[A-Za-z0-9_\-\.]+|Authorization:\s*\S+)`)
	// Password in URL or flag.
	passwordInURLRe = regexp.MustCompile(`://[^/\s]+:[^/\s]+@`)
	passwordFlagRe  = regexp.MustCompile(`(?:--password\s+\S+|-p\s+\S+)`)
	// Long opaque tokens (≥32 alphanumeric characters).
	longTokenRe = regexp.MustCompile(`[A-Za-z0-9]{32,}`)
)

// Scan checks the input text for API keys, private keys, tokens, passwords,
// and other secret patterns using regular expression matching.
func (r *SecretLeakageRule) Scan(_ context.Context, input ScanInput, _ PolicyFile) []Finding {
	text := normalizedScanText(input)
	var findings []Finding

	check := func(re *regexp.Regexp, label, rec string) {
		if re.FindString(text) != "" {
			findings = append(findings, Finding{
				RuleID:         r.ID(),
				RuleName:       r.Name(),
				RiskLevel:      RiskLevelHigh,
				Decision:       DecisionDeny,
				Evidence:       label,
				Recommendation: rec,
			})
		}
	}

	check(apiKeyPrefixRe, "API key prefix detected",
		"Remove hardcoded API keys; use environment variables or secret stores.")
	check(awsKeyRe, "AWS access key ID detected",
		"Remove hardcoded AWS keys; use IAM roles or environment variables.")
	check(privateKeyRe, "Private key detected",
		"Do not embed private keys in commands; use secret management.")
	check(bearerTokenRe, "Bearer/Authorization token detected",
		"Remove hardcoded tokens; pass them through secure channels.")
	check(passwordInURLRe, "Password in URL detected",
		"Remove credentials from URLs; use authentication headers or secret stores.")
	check(passwordFlagRe, "Password flag detected",
		"Avoid passing passwords as command-line arguments; use environment variables or config files.")

	// Only flag long tokens if another secret pattern was not already found
	// to reduce false positives on harmless long hashes.
	if len(findings) == 0 && longTokenRe.FindString(text) != "" {
		findings = append(findings, Finding{
			RuleID:         r.ID(),
			RuleName:       r.Name(),
			RiskLevel:      RiskLevelHigh,
			Decision:       DecisionDeny,
			Evidence:       "long opaque token (≥32 chars)",
			Recommendation: "Verify this is not a leaked secret; remove or mask it.",
		})
	}

	return findings
}

// ---------------------------------------------------------------------------
// R-CMD-001  AllowListMissRule
// ---------------------------------------------------------------------------

// AllowListMissRule flags commands that are not in the allowed-commands list.
// Rule ID: R-CMD-001.
type AllowListMissRule struct{}

// ID returns the rule identifier "R-CMD-001".
func (r *AllowListMissRule) ID() string { return "R-CMD-001" }

// Name returns the human-readable rule name.
func (r *AllowListMissRule) Name() string { return "Allow-List Miss" }

// Scan denies commands whose executable is not in the policy's AllowedCommands
// list. Only active when AllowedCommands is non-empty.
func (r *AllowListMissRule) Scan(_ context.Context, input ScanInput, policy PolicyFile) []Finding {
	// Only active when AllowedCommands is non-empty.
	if len(policy.AllowedCommands) == 0 {
		return nil
	}

	if input.Command == "" {
		return nil
	}

	pipe, err := shellsafe.Parse(input.Command)
	if err != nil {
		// If we cannot parse, the command contains unsafe constructs.
		// ShellBypassRule will handle that; here we just skip.
		return nil
	}

	for _, argv := range pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		if !isCommandAllowed(argv[0], policy.AllowedCommands) {
			return []Finding{{
				RuleID:         r.ID(),
				RuleName:       r.Name(),
				RiskLevel:      RiskLevelMedium,
				Decision:       DecisionDeny,
				Evidence:       argv[0],
				Recommendation: "Add the command to allowed_commands in the policy file.",
			}}
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// R-ENV-001  EnvPolicyRule
// ---------------------------------------------------------------------------

// EnvPolicyRule detects environment variable policy violations.
// Rule ID: R-ENV-001.
type EnvPolicyRule struct{}

// ID returns the rule identifier "R-ENV-001".
func (r *EnvPolicyRule) ID() string { return "R-ENV-001" }

// Name returns the human-readable rule name.
func (r *EnvPolicyRule) Name() string { return "Env Policy" }

// Scan checks environment variables against denied and allowed lists.
func (r *EnvPolicyRule) Scan(_ context.Context, input ScanInput, policy PolicyFile) []Finding {
	var findings []Finding

	// Check denied env vars.
	for _, denied := range policy.DeniedEnvVars {
		if _, ok := input.Env[denied]; ok {
			findings = append(findings, Finding{
				RuleID:         r.ID(),
				RuleName:       r.Name(),
				RiskLevel:      RiskLevelMedium,
				Decision:       DecisionDeny,
				Evidence:       "env var " + denied + " is explicitly denied",
				Recommendation: "Remove the denied environment variable from the execution context.",
			})
		}
	}

	// When AllowedEnvVars is non-empty, check for vars not in the allow list.
	if len(policy.AllowedEnvVars) > 0 {
		allowedSet := make(map[string]struct{}, len(policy.AllowedEnvVars))
		for _, k := range policy.AllowedEnvVars {
			allowedSet[k] = struct{}{}
		}
		for k := range input.Env {
			if _, ok := allowedSet[k]; !ok {
				findings = append(findings, Finding{
					RuleID:         r.ID(),
					RuleName:       r.Name(),
					RiskLevel:      RiskLevelMedium,
					Decision:       DecisionDeny,
					Evidence:       "env var " + k + " is not in allowed_env_vars",
					Recommendation: "Add the environment variable to allowed_env_vars or remove it.",
				})
			}
		}
	}

	return findings
}

// ---------------------------------------------------------------------------
// R-ASK-001  AskForReviewRule
// ---------------------------------------------------------------------------

// AskForReviewRule flags tools that require human review.
// Rule ID: R-ASK-001.
type AskForReviewRule struct{}

// ID returns the rule identifier "R-ASK-001".
func (r *AskForReviewRule) ID() string { return "R-ASK-001" }

// Name returns the human-readable rule name.
func (r *AskForReviewRule) Name() string { return "Ask For Review" }

// Scan flags tool calls whose name appears in the policy's AskForReviewTools list.
func (r *AskForReviewRule) Scan(_ context.Context, input ScanInput, policy PolicyFile) []Finding {
	for _, tool := range policy.AskForReviewTools {
		if input.ToolName == tool {
			return []Finding{{
				RuleID:         r.ID(),
				RuleName:       r.Name(),
				RiskLevel:      RiskLevelLow,
				Decision:       DecisionAsk,
				Evidence:       "tool " + tool + " requires human review",
				Recommendation: "Request human review before proceeding with this tool.",
			}}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// normalizePaths normalizes path separators to forward slashes and expands
// common home-directory references so pattern matching is consistent across
// platforms.
func normalizePaths(text string) string {
	if runtime.GOOS == "windows" {
		text = strings.ReplaceAll(text, `\`, "/")
	}
	// Expand HOME-style references for matching.
	text = strings.ReplaceAll(text, "$HOME", "~")
	return text
}

// isCommandAllowed checks whether the executable (or its basename) appears
// in the allowed list. It uses filepath.Base to strip any path prefix.
func isCommandAllowed(cmd string, allowed []string) bool {
	base := filepath.Base(cmd)
	for _, a := range allowed {
		if a == cmd || a == base {
			return true
		}
	}
	return false
}

// isCommandAllowedExecutable parses the command via shellsafe and checks
// whether the first executable in the pipeline appears in the allowed list.
// This matches AllowListMissRule semantics. Falls back to isCommandAllowed
// if the command cannot be parsed.
func isCommandAllowedExecutable(cmd string, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	pipe, err := shellsafe.Parse(cmd)
	if err != nil {
		// Cannot parse the command; fall back to whole-string matching via
		// isCommandAllowed. This asymmetry is intentional and fail-closed:
		// successful parsing requires *every* pipeline command's executable
		// to be allowed, while parse failure only checks the raw string,
		// which is less precise but still conservative.
		return isCommandAllowed(cmd, allowed)
	}
	for _, argv := range pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		if !isCommandAllowed(argv[0], allowed) {
			return false
		}
	}
	return true
}
