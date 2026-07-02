//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "strings"

// combineInput merges Command and CodeBlocks content into a single
// lower-cased string for unified scanning, preventing CodeBlocks-only
// payloads from bypassing the scanner.
func combineInput(input ScanInput) string {
	var parts []string
	if input.Command != "" {
		parts = append(parts, input.Command)
	}
	for _, cb := range input.CodeBlocks {
		if cb.Code != "" {
			parts = append(parts, cb.Code)
		}
	}
	return strings.ToLower(strings.Join(parts, " "))
}

// ---------- Rule 1: Dangerous Command Detection ----------

// DangerousCommandRule checks for explicitly dangerous commands.
//
// It detects:
//   - Destructive filesystem operations (rm -rf, format, dd)
//   - Reading sensitive files (~/.ssh, /etc/passwd, .env)
//   - Commands that leak credentials (cat ~/.aws/credentials)
type DangerousCommandRule struct {
	dangerousCommands []string
	sensitivePaths    []string
}

// NewDangerousCommandRule creates a rule with the default deny list.
func NewDangerousCommandRule() *DangerousCommandRule {
	return &DangerousCommandRule{
		dangerousCommands: []string{
			"rm -rf /", "rm -rf ~", "rm -rf .", "rm -rf /*",
			"dd if=", "mkfs.", "fdisk",
			"shutdown", "reboot", "halt", "poweroff", "init 0", "init 6",
			"chmod 777 /", "chown -R", "setfacl -R",
		},
		sensitivePaths: []string{
			".ssh/id_rsa", ".ssh/authorized", ".aws/credentials", ".gcloud/",
			"credentials.json",
			".env", ".env.", "/etc/shadow", "/etc/passwd",
			".pem", ".key", ".p12", ".pfx", "id_ed25519",
			".sql", ".dump", "backup",
		},
	}
}

// ID returns the unique identifier of this rule.
func (r *DangerousCommandRule) ID() string { return "danger_cmd_001" }

// Check inspects the input for dangerous command keywords and sensitive path access.
func (r *DangerousCommandRule) Check(input ScanInput) *ScanResult {
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, kw := range r.dangerousCommands {
		if strings.Contains(cmd, kw) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskCritical,
				RuleID:    r.ID(),
				Evidence:  kw,
				Reason:    "dangerous command: " + kw,
			}
		}
	}
	for _, p := range r.sensitivePaths {
		if strings.Contains(cmd, p) {
			if strings.Contains(cmd, "cat ") || strings.Contains(cmd, "less ") ||
				strings.Contains(cmd, "head ") || strings.Contains(cmd, "tail ") ||
				strings.Contains(cmd, "grep ") || strings.Contains(cmd, "cp ") {
				return &ScanResult{
					Decision:  DecisionDeny,
					RiskLevel: RiskCritical,
					RuleID:    r.ID(),
					Evidence:  p,
					Reason:    "reading sensitive file: " + p,
				}
			}
			if strings.Contains(cmd, "rm ") || strings.Contains(cmd, "mv ") ||
				strings.Contains(cmd, ">") {
				return &ScanResult{
					Decision:  DecisionDeny,
					RiskLevel: RiskCritical,
					RuleID:    r.ID(),
					Evidence:  p,
					Reason:    "modifying/deleting sensitive file: " + p,
				}
			}
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskHigh,
				RuleID:    r.ID(),
				Evidence:  p,
				Reason:    "accessing sensitive path: " + p,
			}
		}
	}
	return nil
}

// ---------- Rule 2: Network Access Detection ----------

// NetworkAccessRule detects commands that connect to external networks.
//
// It supports an optional allow list of domains: when the rule fires and the
// command's URL/host matches an entry in the allow list, the rule returns
// DecisionAsk instead of DecisionDeny so a human can approve the call.
type NetworkAccessRule struct {
	dangerousCmds  []string
	allowedDomains []string
}

// NewNetworkAccessRule creates a network access detection rule
// with an empty allow list (deny-by-default for any detected network access).
func NewNetworkAccessRule() *NetworkAccessRule {
	return &NetworkAccessRule{
		dangerousCmds: []string{
			"curl", "wget", "nc ", "ncat", "telnet",
			"ssh ", "scp ", "sftp", "rsync",
			"nslookup", "dig ", "host ", "nmap", "socat",
			"git clone", "pip install", "npm install",
		},
	}
}

// NewNetworkAccessRuleWithAllowlist creates a NetworkAccessRule that downgrades
// matches to DecisionAsk when the target domain is in the allow list.
//
// Each entry in allowedDomains may be a bare host ("github.com") or a wildcard
// like "*.example.com". Comparison is case-insensitive.
func NewNetworkAccessRuleWithAllowlist(allowedDomains []string) *NetworkAccessRule {
	r := NewNetworkAccessRule()
	r.allowedDomains = allowedDomains
	return r
}

// WithAllowedDomains sets the allow list on an existing rule.
func (r *NetworkAccessRule) WithAllowedDomains(domains []string) *NetworkAccessRule {
	r.allowedDomains = domains
	return r
}

// ID returns the unique identifier of this rule.
func (r *NetworkAccessRule) ID() string { return "network_002" }

// Check inspects the input for network access keywords.
//
// If a keyword matches, the rule inspects the command for any URL/host
// arguments. When the host is in the configured allow list, the rule returns
// DecisionAsk (human review) instead of DecisionDeny.
func (r *NetworkAccessRule) Check(input ScanInput) *ScanResult {
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, kw := range r.dangerousCmds {
		if !strings.Contains(cmd, kw) {
			continue
		}
		// If a host in the command matches the allow list, downgrade to ask.
		if r.matchesAllowlist(cmd) {
			return &ScanResult{
				Decision:  DecisionAsk,
				RiskLevel: RiskMedium,
				RuleID:    r.ID(),
				Evidence:  kw,
				Reason:    "network access to allowlisted host: " + kw,
			}
		}
		return &ScanResult{
			Decision:  DecisionDeny,
			RiskLevel: RiskHigh,
			RuleID:    r.ID(),
			Evidence:  kw,
			Reason:    "network access: " + kw,
		}
	}
	return nil
}

// matchesAllowlist reports whether cmd contains a host that is in the
// configured allow list. The match is case-insensitive and supports the
// wildcard prefix "*.".
func (r *NetworkAccessRule) matchesAllowlist(cmd string) bool {
	if len(r.allowedDomains) == 0 {
		return false
	}
	lower := strings.ToLower(cmd)
	for _, pattern := range r.allowedDomains {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		// Wildcard suffix: "*.example.com" matches "foo.example.com".
		if strings.HasPrefix(pattern, "*.") {
			suffix := pattern[1:] // ".example.com"
			if strings.Contains(lower, suffix) {
				return true
			}
			continue
		}
		// Plain substring match against the lower-cased command.
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// ---------- Rule 3: Shell Bypass Detection ----------

// ShellBypassRule detects attempts to bypass shell safety restrictions
// by using -c flags, eval, or other indirect execution methods.
type ShellBypassRule struct {
	bypassPatterns []string
}

// NewShellBypassRule creates a shell bypass detection rule.
func NewShellBypassRule() *ShellBypassRule {
	return &ShellBypassRule{
		bypassPatterns: []string{
			"sh -c", "bash -c", "zsh -c",
			"python -c", "python3 -c",
			"perl -e", "ruby -e", "node -e",
			"eval ", "exec ", "source ", "xargs ",
			"env ", "sudo ", "su ",
			"base64 -d", "xxd -r",
			"/dev/tcp/", "/dev/udp/",
		},
	}
}

// ID returns the unique identifier of this rule.
func (r *ShellBypassRule) ID() string { return "shell_bypass_003" }

// Check inspects the input for shell bypass patterns.
func (r *ShellBypassRule) Check(input ScanInput) *ScanResult {
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, p := range r.bypassPatterns {
		if strings.Contains(cmd, p) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskHigh,
				RuleID:    r.ID(),
				Evidence:  p,
				Reason:    "shell bypass attempt: " + p,
			}
		}
	}
	return nil
}

// ---------- Rule 4: Install and System Mutation Detection ----------

// InstallAndMutateRule detects package manager installs and system config changes.
type InstallAndMutateRule struct{ patterns []string }

// NewInstallAndMutateRule creates an install/mutation detection rule.
func NewInstallAndMutateRule() *InstallAndMutateRule {
	return &InstallAndMutateRule{patterns: []string{
		"apt install", "apt-get install", "apt-get update",
		"yum install", "dnf install",
		"pacman -S", "brew install",
		"npm install", "npm i ",
		"pip install", "pip3 install",
		"go install", "go get ",
		"gem install", "cargo install",
		"snap install", "flatpak install",
		"systemctl enable", "systemctl start",
		"service start", "service enable",
		"update-rc.d", "crontab ",
		"iptables ", "nft ",
	}}
}

// ID returns the unique identifier of this rule.
func (r *InstallAndMutateRule) ID() string { return "install_004" }

// Check inspects the input for install or system mutation patterns.
func (r *InstallAndMutateRule) Check(input ScanInput) *ScanResult {
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, p := range r.patterns {
		if strings.Contains(cmd, p) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskMedium,
				RuleID:    r.ID(),
				Evidence:  p,
				Reason:    "install or system mutation: " + p,
			}
		}
	}
	return nil
}

// ---------- Rule 5: Host Execution Risk Detection ----------

// HostExecRiskRule detects host-level operations that only apply to the local executor.
type HostExecRiskRule struct{ risks []string }

// NewHostExecRiskRule creates a host execution risk detection rule.
func NewHostExecRiskRule() *HostExecRiskRule {
	return &HostExecRiskRule{risks: []string{
		"tty", "pty",
		"nohup ", "disown", "bg ", "fg ",
		"daemon", "fork",
		"sudo ", "su -", "su root",
		"chmod 777", "chmod -R 777",
		"chown root", "chown :root",
		"setuid", "setgid",
		"insmod ", "modprobe ", "rmmod ",
		"mount ", "umount ",
	}}
}

// ID returns the unique identifier of this rule.
func (r *HostExecRiskRule) ID() string { return "hostexec_005" }

// Check inspects the input for host execution risk keywords. Skipped for container executors.
func (r *HostExecRiskRule) Check(input ScanInput) *ScanResult {
	if input.ExecutorType != "local" && input.ExecutorType != "" {
		return nil
	}
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, risk := range r.risks {
		if strings.Contains(cmd, risk) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskCritical,
				RuleID:    r.ID(),
				Evidence:  risk,
				Reason:    "host execution risk: " + risk,
			}
		}
	}
	return nil
}

// ---------- Rule 6: Resource Abuse Detection ----------

// ResourceAbuseRule detects resource exhaustion patterns such as infinite loops
// and fork bombs.
type ResourceAbuseRule struct{}

// NewResourceAbuseRule creates a resource abuse detection rule.
func NewResourceAbuseRule() *ResourceAbuseRule { return &ResourceAbuseRule{} }

// ID returns the unique identifier of this rule.
func (r *ResourceAbuseRule) ID() string { return "resource_006" }

// Check inspects the input for resource abuse patterns.
func (r *ResourceAbuseRule) Check(input ScanInput) *ScanResult {
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, lp := range []string{"while true", "while :", "for (( ; ; ))", "while [ 1 ]", "while [[ 1 ]]"} {
		if strings.Contains(cmd, lp) {
			return &ScanResult{Decision: DecisionDeny, RiskLevel: RiskHigh, RuleID: r.ID(), Evidence: lp, Reason: "infinite loop: " + lp}
		}
	}
	for _, fp := range []string{":(){ :|:& };:", "() {"} {
		if strings.Contains(cmd, fp) {
			return &ScanResult{Decision: DecisionDeny, RiskLevel: RiskCritical, RuleID: r.ID(), Evidence: fp, Reason: "fork bomb pattern"}
		}
	}
	for _, rc := range []string{"stress ", "stress-ng", "yes ", "dd if=/dev/zero of=", ">/dev/null", ": >", "sha256sum /dev/zero", "md5sum /dev/zero"} {
		if strings.Contains(cmd, rc) {
			return &ScanResult{Decision: DecisionDeny, RiskLevel: RiskHigh, RuleID: r.ID(), Evidence: rc, Reason: "resource exhaustion: " + rc}
		}
	}
	return nil
}

// ---------- Rule 7: Sensitive Information Leak Detection ----------

// SensitiveInfoLeakRule detects patterns that may leak credentials or sensitive data to files.
type SensitiveInfoLeakRule struct{ patterns []string }

// NewSensitiveInfoLeakRule creates a sensitive info leak detection rule.
func NewSensitiveInfoLeakRule() *SensitiveInfoLeakRule {
	return &SensitiveInfoLeakRule{patterns: []string{
		"api_key", "apikey", "api_secret", "apisecret",
		"access_key", "secret_key", "private_key", "privatekey",
		"password", "passwd", "passphrase",
		"db_password", "db_pass",
		"token", "bearer", "jwt",
		"auth_token", "refresh_token",
	}}
}

// ID returns the unique identifier of this rule.
func (r *SensitiveInfoLeakRule) ID() string { return "leak_007" }

// Check inspects the input for sensitive information leakage patterns.
func (r *SensitiveInfoLeakRule) Check(input ScanInput) *ScanResult {
	all := combineInput(input)
	if all == "" {
		return nil
	}
	for _, p := range r.patterns {
		if !strings.Contains(all, p) {
			continue
		}
		if (strings.Contains(all, "echo ") || strings.Contains(all, "cat ") ||
			strings.Contains(all, "printf ")) && strings.Contains(all, ">") {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskCritical,
				RuleID:    r.ID(),
				Evidence:  p,
				Reason:    "sensitive info leak: " + p,
			}
		}
	}
	return nil
}

// ---------- Rule 8: Human Review (ask decision) ----------

// AskForReviewRule returns DecisionAsk for commands that are
// potentially risky but may have legitimate use cases.
type AskForReviewRule struct{ patterns []string }

// NewAskForReviewRule creates a rule that returns ask for risky-but-legitimate commands.
func NewAskForReviewRule() *AskForReviewRule {
	return &AskForReviewRule{patterns: []string{
		"rm -r", "git push", "docker push",
		"kubectl delete", "drop table", "truncate ",
		"force",
	}}
}

// ID returns the unique identifier of this rule.
func (r *AskForReviewRule) ID() string { return "ask_review_008" }

// Check inspects the input for risky-but-legitimate commands requiring human review.
func (r *AskForReviewRule) Check(input ScanInput) *ScanResult {
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, p := range r.patterns {
		if strings.Contains(cmd, p) {
			return &ScanResult{
				Decision:  DecisionAsk,
				RiskLevel: RiskMedium,
				RuleID:    r.ID(),
				Evidence:  p,
				Reason:    "requires human review: " + p,
			}
		}
	}
	return nil
}

// ---------- Rule 9: Parse Failure (fail-closed) ----------

// ParseFailureRule denies any command that the shellsafe parser
// rejects. This is the "fail-closed" half of the safety contract:
// a structurally unsafe command (variable expansion, command
// substitution, subshells, ...) must never reach the executor even
// if no other rule fires.
//
// Empty input is treated as "no rule applies" and returns nil, so
// non-command code blocks (e.g. pure documentation snippets) are
// not penalised.
type ParseFailureRule struct{}

// NewParseFailureRule creates a rule that fails closed on parser errors.
func NewParseFailureRule() *ParseFailureRule { return &ParseFailureRule{} }

// ID returns the unique identifier of this rule.
func (r *ParseFailureRule) ID() string { return "parse_fail_009" }

// Check parses the input.Command via shellsafe. On any parse error
// the rule returns DecisionDeny with RiskCritical.
func (r *ParseFailureRule) Check(input ScanInput) *ScanResult {
	cmd := strings.TrimSpace(input.Command)
	if cmd == "" {
		return nil
	}
	if _, err := ParseCommand(cmd); err != nil {
		return &ScanResult{
			Decision:  DecisionDeny,
			RiskLevel: RiskCritical,
			RuleID:    r.ID(),
			Evidence:  err.Error(),
			Reason:    "unparsable command (shellsafe rejected): " + err.Error(),
		}
	}
	return nil
}

// ---------- Rule 10: Shell Wrapper Detection (structural) ----------

// ShellWrapperRule denies commands whose pipeline starts with a
// shell wrapper / re-executing builtin (sh, bash, sudo, xargs,
// eval, ...). The check is structural - it inspects argv[0] of
// every segment via the shellsafe parser rather than scanning the
// raw command text, so encoded / wrapped forms ("$(echo sh)",
// "/usr/bin/SH", "sh.exe", "${X}sh") cannot smuggle past.
//
// Empty input is treated as "no rule applies" and returns nil.
type ShellWrapperRule struct{}

// NewShellWrapperRule creates a rule that denies shell wrappers.
func NewShellWrapperRule() *ShellWrapperRule { return &ShellWrapperRule{} }

// ID returns the unique identifier of this rule.
func (r *ShellWrapperRule) ID() string { return "shell_wrapper_010" }

// Check parses the input.Command via shellsafe and rejects any
// segment whose argv[0] is a known shell wrapper.
//
// If the command cannot be parsed at all, the rule returns nil so it
// does not double-report the failure (that is ParseFailureRule's job).
// The fail-closed behaviour is still preserved because the parser
// will emit a separate deny.
func (r *ShellWrapperRule) Check(input ScanInput) *ScanResult {
	cmd := strings.TrimSpace(input.Command)
	if cmd == "" {
		return nil
	}
	parsed, err := ParseCommand(cmd)
	if err != nil {
		return nil
	}
	for _, seg := range parsed.Segments {
		if len(seg) == 0 {
			continue
		}
		if IsShellWrapper(seg[0]) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskCritical,
				RuleID:    r.ID(),
				Evidence:  seg[0],
				Reason:    "shell wrapper detected: " + seg[0],
			}
		}
	}
	return nil
}
