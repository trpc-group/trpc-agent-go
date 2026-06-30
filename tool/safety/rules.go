//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"strings"
)

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

// ---------- Rule 1: 危险命令检测 ----------

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
func (r *DangerousCommandRule) ID() string {
	return "danger_cmd_001"
}

// Check inspects the input for dangerous command keywords and sensitive path access.
func (r *DangerousCommandRule) Check(input ScanInput) *ScanResult {
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, keyword := range r.dangerousCommands {
		if strings.Contains(cmd, keyword) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskCritical,
				RuleID:    r.ID(),
				Evidence:  keyword,
				Reason:    "命令包含危险操作：" + keyword + "。该命令可能导致系统损坏或数据丢失。",
			}
		}
	}
	for _, path := range r.sensitivePaths {
		if strings.Contains(cmd, path) {
			if strings.Contains(cmd, "cat ") || strings.Contains(cmd, "less ") ||
				strings.Contains(cmd, "head ") || strings.Contains(cmd, "tail ") ||
				strings.Contains(cmd, "grep ") || strings.Contains(cmd, "cp ") {
				return &ScanResult{
					Decision:  DecisionDeny,
					RiskLevel: RiskCritical,
					RuleID:    r.ID(),
					Evidence:  path,
					Reason:    "尝试读取敏感文件：" + path + "。该文件可能包含凭据或隐私信息。",
				}
			}
			if strings.Contains(cmd, "rm ") || strings.Contains(cmd, "mv ") ||
				strings.Contains(cmd, ">") {
				return &ScanResult{
					Decision:  DecisionDeny,
					RiskLevel: RiskCritical,
					RuleID:    r.ID(),
					Evidence:  path,
					Reason:    "尝试修改/删除敏感文件：" + path + "。",
				}
			}
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskHigh,
				RuleID:    r.ID(),
				Evidence:  path,
				Reason:    "尝试访问敏感路径：" + path + "。",
			}
		}
	}
	return nil
}

// ---------- Rule 2: 网络外连检测 ----------

// NetworkAccessRule detects commands that connect to external networks.
type NetworkAccessRule struct {
	dangerousCmds []string
}

// NewNetworkAccessRule creates a network access detection rule.
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

// ID returns the unique identifier of this rule.
func (r *NetworkAccessRule) ID() string {
	return "network_002"
}

// Check inspects the input for network access keywords.
func (r *NetworkAccessRule) Check(input ScanInput) *ScanResult {
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, keyword := range r.dangerousCmds {
		if strings.Contains(cmd, keyword) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskHigh,
				RuleID:    r.ID(),
				Evidence:  keyword,
				Reason:    "命令尝试进行网络访问：" + keyword + "。可能将数据外传或下载恶意内容。",
			}
		}
	}
	return nil
}

// ---------- Rule 3: Shell 绕过检测 ----------

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
func (r *ShellBypassRule) ID() string {
	return "shell_bypass_003"
}

// Check inspects the input for shell bypass patterns.
func (r *ShellBypassRule) Check(input ScanInput) *ScanResult {
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, pattern := range r.bypassPatterns {
		if strings.Contains(cmd, pattern) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskHigh,
				RuleID:    r.ID(),
				Evidence:  pattern,
				Reason:    "命令尝试绕过 shell 安全检查：" + pattern + "。可能通过间接执行方式运行任意命令。",
			}
		}
	}
	return nil
}

// ---------- Rule 4: 依赖安装与系统变更检测 ----------

// InstallAndMutateRule detects package manager installs and system config changes.
type InstallAndMutateRule struct {
	patterns []string
}

// NewInstallAndMutateRule creates an install/mutation detection rule.
func NewInstallAndMutateRule() *InstallAndMutateRule {
	return &InstallAndMutateRule{
		patterns: []string{
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
			"update-rc.d",
			"crontab ",
			"iptables ", "nft ",
		},
	}
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
				Reason:    "命令尝试安装软件或修改系统配置：" + p + "。可能引入恶意依赖或破坏系统稳定性。",
			}
		}
	}
	return nil
}

// ---------- Rule 5: 宿主机执行风险检测 ----------

// HostExecRiskRule detects host-level operations that only apply to the local executor.
type HostExecRiskRule struct {
	risks []string
}

// NewHostExecRiskRule creates a host execution risk detection rule.
func NewHostExecRiskRule() *HostExecRiskRule {
	return &HostExecRiskRule{
		risks: []string{
			"tty", "pty",
			"nohup ", "disown", "bg ", "fg ",
			"daemon", "fork",
			"sudo ", "su -", "su root",
			"chmod 777", "chmod -R 777",
			"chown root", "chown :root",
			"setuid", "setgid",
			"insmod ", "modprobe ", "rmmod ",
			"mount ", "umount ",
		},
	}
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
				Reason:    "命令包含宿主机执行风险操作：" + risk + "。可能影响宿主机安全或稳定性。",
			}
		}
	}
	return nil
}

// ---------- Rule 6: 资源滥用检测 ----------

// ResourceAbuseRule detects resource exhaustion patterns such as infinite loops
// and fork bombs.
type ResourceAbuseRule struct{}

// NewResourceAbuseRule creates a resource abuse detection rule.
func NewResourceAbuseRule() *ResourceAbuseRule {
	return &ResourceAbuseRule{}
}

// ID returns the unique identifier of this rule.
func (r *ResourceAbuseRule) ID() string { return "resource_006" }

// Check inspects the input for resource abuse patterns.
func (r *ResourceAbuseRule) Check(input ScanInput) *ScanResult {
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	loopPatterns := []string{
		"while true", "while :", "for (( ; ; ))",
		"while [ 1 ]", "while [[ 1 ]]",
	}
	for _, lp := range loopPatterns {
		if strings.Contains(cmd, lp) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskHigh,
				RuleID:    r.ID(),
				Evidence:  lp,
				Reason:    "命令包含无限循环：" + lp + "。可能导致 CPU 或内存资源耗尽。",
			}
		}
	}
	fbPatterns := []string{
		":(){ :|:& };:",
		"() {",
	}
	for _, fp := range fbPatterns {
		if strings.Contains(cmd, fp) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskCritical,
				RuleID:    r.ID(),
				Evidence:  fp,
				Reason:    "命令包含 fork bomb 模式，可能导致系统崩溃。",
			}
		}
	}
	resourceCmds := []string{
		"stress ", "stress-ng",
		"yes ", "dd if=/dev/zero of=",
		">/dev/null", ": >",
		"sha256sum /dev/zero", "md5sum /dev/zero",
	}
	for _, rc := range resourceCmds {
		if strings.Contains(cmd, rc) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskHigh,
				RuleID:    r.ID(),
				Evidence:  rc,
				Reason:    "命令尝试消耗大量系统资源：" + rc + "。",
			}
		}
	}
	return nil
}

// ---------- Rule 7: 敏感信息泄漏检测 ----------

// SensitiveInfoLeakRule detects patterns that may leak credentials
// or sensitive data to files.
type SensitiveInfoLeakRule struct {
	patterns []string
}

// NewSensitiveInfoLeakRule creates a sensitive info leak detection rule.
func NewSensitiveInfoLeakRule() *SensitiveInfoLeakRule {
	return &SensitiveInfoLeakRule{
		patterns: []string{
			"api_key", "apikey", "api_secret", "apisecret",
			"access_key", "secret_key",
			"private_key", "privatekey",
			"password", "passwd", "passphrase",
			"db_password", "db_pass",
			"token", "bearer", "jwt",
			"auth_token", "refresh_token",
			" > ", ">>",
		},
	}
}

// ID returns the unique identifier of this rule.
func (r *SensitiveInfoLeakRule) ID() string { return "leak_007" }

// Check inspects the input for sensitive information leakage patterns.
func (r *SensitiveInfoLeakRule) Check(input ScanInput) *ScanResult {
	allText := combineInput(input)
	if allText == "" {
		return nil
	}
	for _, p := range r.patterns {
		if !strings.Contains(allText, p) {
			continue
		}
		if (strings.Contains(allText, "echo ") || strings.Contains(allText, "cat ") ||
			strings.Contains(allText, "printf ")) && strings.Contains(allText, ">") {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskCritical,
				RuleID:    r.ID(),
				Evidence:  p,
				Reason:    "命令可能将敏感信息写入文件：" + p + "。存在凭据泄漏风险。",
			}
		}
	}
	return nil
}

// ---------- Rule 8: 人工复核（ask 决策）----------

// AskForReviewRule returns DecisionAsk for commands that are
// potentially risky but may have legitimate use cases.
type AskForReviewRule struct {
	patterns []string
}

// NewAskForReviewRule creates a rule that returns ask for risky-but-legitimate commands.
func NewAskForReviewRule() *AskForReviewRule {
	return &AskForReviewRule{
		patterns: []string{
			"rm -r", "git push", "docker push",
			"kubectl delete", "drop table", "truncate ",
			"force",
		},
	}
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
				Reason:    "命令需要人工审核：" + p + "。该操作可能合法，但存在风险。",
			}
		}
	}
	return nil
}
