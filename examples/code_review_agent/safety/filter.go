// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package safety 提供命令执行的安全检查和拦截能力。
//
// 基于 trpc-agent-go 的 tool.PermissionPolicy 概念，
// 在沙箱执行前对命令进行风险评估，返回 allow / deny / ask 决策。
//
// 支持从 YAML/JSON 配置文件加载规则，方便用户自定义。
package safety

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ========== 决策类型 ==========

// Decision 安全决策类型。
type Decision string

const (
	DecisionAllow Decision = "allow" // 允许执行
	DecisionDeny  Decision = "deny"  // 拒绝执行
	DecisionAsk   Decision = "ask"   // 需要人工确认
)

// SafetyDecision 安全决策结果。
type SafetyDecision struct {
	Decision  Decision `json:"decision"`   // allow / deny / ask
	Reason    string   `json:"reason"`     // 决策原因
	RiskLevel string   `json:"risk_level"` // high / medium / low / safe
	RuleID    string   `json:"rule_id"`    // 命中的规则 ID
	Command   string   `json:"command"`    // 被检查的命令
}

// ========== 安全过滤器 ==========

// SafetyFilter 命令安全过滤器。
type SafetyFilter struct {
	config *SafetyConfig
	logger *AuditLogger
}

// SafetyConfig 安全过滤器配置。
type SafetyConfig struct {
	// 白名单命令（允许执行）
	AllowedCommands []string `json:"allowed_commands"`

	// 黑名单命令（直接拒绝）
	DeniedCommands []string `json:"denied_commands"`

	// 危险路径（不允许访问的路径）
	DeniedPaths []string `json:"denied_paths"`

	// 网络白名单域名
	AllowedDomains []string `json:"allowed_domains"`

	// 最大超时时间
	MaxTimeout time.Duration `json:"max_timeout"`

	// 最大输出大小
	MaxOutput int `json:"max_output"`

	// 环境变量白名单
	AllowedEnvVars []string `json:"allowed_env_vars"`

	// 默认策略
	DefaultPolicy Decision `json:"default_policy"`

	// 日志文件路径（空则不写文件）
	LogFile string `json:"log_file"`
}

// DefaultConfig 返回默认的安全配置。
func DefaultConfig() *SafetyConfig {
	return &SafetyConfig{
		AllowedCommands: []string{
			// Go 工具链
			"go test", "go vet", "go build", "go mod",
			"go fmt", "goimports", "gofmt",
			// 静态分析
			"staticcheck", "golangci-lint", "golint", "errcheck",
			// 通用工具
			"ls", "cat", "head", "tail", "grep", "find", "wc",
			"echo", "pwd", "env",
		},
		DeniedCommands: []string{
			// 危险删除
			"rm -rf", "rm -r /", "rm -f /",
			// 系统目录操作
			"chmod 777", "chown", "mkfs",
			// 网络工具（外连）
			"curl ", "wget ", "nc ", "ncat", "socat",
			// 包管理（改变环境）
			"apt install", "apt-get install", "yum install",
			"pip install", "npm install", "go install ",
			// 提权
			"sudo ", "su ",
			// 编辑器
			"vim ", "nano ", "emacs ",
		},
		DeniedPaths: []string{
			"/etc/", "/root/", "/home/", "~/.ssh", "~/.aws",
			"~/.gnupg", "/var/", "/usr/", "/boot/", "/sys/", "/proc/",
		},
		AllowedDomains: []string{
			"proxy.golang.org", "sum.golang.org",
			"goproxy.cn", "goproxy.io",
			"github.com", "raw.githubusercontent.com",
		},
		MaxTimeout: 5 * time.Minute,
		MaxOutput:  5 * 1024 * 1024,
		AllowedEnvVars: []string{
			"PATH", "HOME", "USER", "SHELL",
			"GOPATH", "GOROOT", "GOPROXY", "GOFLAGS",
			"GONOSUMCHECK", "GONOSUMDB", "GOPRIVATE",
			"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
			"LANG", "LC_ALL", "TERM",
		},
		DefaultPolicy: DecisionAllow,
	}
}

// ========== 配置文件加载 ==========

// LoadConfig 从 JSON 文件加载配置。
//
// 如果文件不存在，返回默认配置。
// 如果文件存在但解析失败，返回错误。
func LoadConfig(path string) (*SafetyConfig, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return DefaultConfig(), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	config := DefaultConfig() // 先加载默认值
	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	return config, nil
}

// SaveConfig 保存配置到 JSON 文件。
func SaveConfig(config *SafetyConfig, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// ========== 构造函数 ==========

// NewSafetyFilter 创建安全过滤器。
func NewSafetyFilter(config *SafetyConfig) *SafetyFilter {
	if config == nil {
		config = DefaultConfig()
	}

	f := &SafetyFilter{config: config}

	// 初始化日志
	if config.LogFile != "" {
		f.logger = NewAuditLogger(config.LogFile)
	}

	return f
}

// NewSafetyFilterWithLogger 创建带自定义日志的安全过滤器。
func NewSafetyFilterWithLogger(config *SafetyConfig, logger *AuditLogger) *SafetyFilter {
	if config == nil {
		config = DefaultConfig()
	}
	return &SafetyFilter{config: config, logger: logger}
}

// ========== 检查方法 ==========

// Check 检查命令是否安全。
//
// 检查流程（顺序很重要）：
//  1. 空命令 → deny
//  2. 黑名单匹配 → deny
//  3. 危险路径访问 → deny
//  4. Shell 注入检查 → ask
//  5. 网络外连检查 → deny / allow
//  6. 白名单匹配 → allow
//  7. 默认策略
func (f *SafetyFilter) Check(command string) *SafetyDecision {
	command = strings.TrimSpace(command)

	decision := f.checkInternal(command)

	// 记录审计日志
	if f.logger != nil {
		f.logger.Log(decision)
	}

	return decision
}

func (f *SafetyFilter) checkInternal(command string) *SafetyDecision {
	// 1. 空命令
	if command == "" {
		return &SafetyDecision{
			Decision:  DecisionDeny,
			Reason:    "命令不能为空",
			RiskLevel: "safe",
			RuleID:    "EMPTY_CMD",
			Command:   command,
		}
	}

	// 2. 黑名单检查
	if denied, ruleID := f.isDenied(command); denied {
		return &SafetyDecision{
			Decision:  DecisionDeny,
			Reason:    "命令在黑名单中",
			RiskLevel: "high",
			RuleID:    ruleID,
			Command:   command,
		}
	}

	// 3. 危险路径访问
	if path, ruleID := f.accessesDeniedPath(command); path != "" {
		return &SafetyDecision{
			Decision:  DecisionDeny,
			Reason:    fmt.Sprintf("不允许访问路径: %s", path),
			RiskLevel: "high",
			RuleID:    ruleID,
			Command:   command,
		}
	}

	// 4. Shell 注入检查
	if pattern := f.hasShellInjection(command); pattern != "" {
		return &SafetyDecision{
			Decision:  DecisionAsk,
			Reason:    fmt.Sprintf("检测到可能的 shell 注入模式: %s", pattern),
			RiskLevel: "medium",
			RuleID:    "SHELL_INJECTION",
			Command:   command,
		}
	}

	// 5. 网络外连检查
	if domain := f.hasNetworkAccess(command); domain != "" {
		if f.isDomainAllowed(domain) {
			return &SafetyDecision{
				Decision:  DecisionAllow,
				Reason:    fmt.Sprintf("域名 %s 在白名单中", domain),
				RiskLevel: "low",
				RuleID:    "NET_ALLOWED",
				Command:   command,
			}
		}
		return &SafetyDecision{
			Decision:  DecisionDeny,
			Reason:    fmt.Sprintf("不允许访问非白名单域名: %s", domain),
			RiskLevel: "high",
			RuleID:    "NET_DENIED",
			Command:   command,
		}
	}

	// 6. 白名单检查
	if f.isAllowed(command) {
		return &SafetyDecision{
			Decision:  DecisionAllow,
			Reason:    "命令在白名单中",
			RiskLevel: "safe",
			RuleID:    "WHITELIST",
			Command:   command,
		}
	}

	// 7. 默认策略
	return &SafetyDecision{
		Decision:  f.config.DefaultPolicy,
		Reason:    "未匹配任何规则，使用默认策略",
		RiskLevel: "low",
		RuleID:    "DEFAULT",
		Command:   command,
	}
}

// CheckWithOptions 检查命令并验证执行选项。
func (f *SafetyFilter) CheckWithOptions(command string, timeout time.Duration, maxOutput int) *SafetyDecision {
	decision := f.Check(command)
	if decision.Decision != DecisionAllow {
		return decision
	}

	if timeout > f.config.MaxTimeout {
		return &SafetyDecision{
			Decision:  DecisionDeny,
			Reason:    fmt.Sprintf("超时时间 %v 超过最大限制 %v", timeout, f.config.MaxTimeout),
			RiskLevel: "medium",
			RuleID:    "TIMEOUT_EXCEEDED",
			Command:   command,
		}
	}

	if maxOutput > f.config.MaxOutput {
		return &SafetyDecision{
			Decision:  DecisionDeny,
			Reason:    fmt.Sprintf("输出大小限制 %d 超过最大限制 %d", maxOutput, f.config.MaxOutput),
			RiskLevel: "medium",
			RuleID:    "OUTPUT_EXCEEDED",
			Command:   command,
		}
	}

	return decision
}

// FilterEnvVars 过滤环境变量，只保留白名单中的。
func (f *SafetyFilter) FilterEnvVars(env map[string]string) map[string]string {
	if len(f.config.AllowedEnvVars) == 0 {
		return env
	}

	allowed := make(map[string]bool)
	for _, v := range f.config.AllowedEnvVars {
		allowed[strings.ToUpper(v)] = true
	}

	filtered := make(map[string]string)
	for k, v := range env {
		if allowed[strings.ToUpper(k)] {
			filtered[k] = v
		}
	}
	return filtered
}

// ========== 内部检查方法 ==========

// isAllowed 检查命令是否在白名单中。
func (f *SafetyFilter) isAllowed(command string) bool {
	lower := strings.ToLower(command)
	for _, allowed := range f.config.AllowedCommands {
		// 精确前缀匹配：命令必须以白名单项开头
		// "go test" 匹配 "go test ./..." 但不匹配 "go test-dangerous"
		if strings.HasPrefix(lower, strings.ToLower(allowed)) {
			// 检查匹配后的下一个字符（如果有）
			endIdx := len(allowed)
			if endIdx < len(lower) {
				nextChar := lower[endIdx]
				// 下一个字符必须是空格、行尾或特殊字符
				if nextChar != ' ' && nextChar != '\t' && nextChar != '\n' &&
					nextChar != '|' && nextChar != '&' && nextChar != ';' {
					continue
				}
			}
			return true
		}
	}
	return false
}

// isDenied 检查命令是否在黑名单中。
func (f *SafetyFilter) isDenied(command string) (bool, string) {
	lower := strings.ToLower(command)
	for _, denied := range f.config.DeniedCommands {
		deniedLower := strings.ToLower(denied)
		// 检查黑名单项是否在命令中出现
		if strings.Contains(lower, deniedLower) {
			// 验证是独立的命令/参数（不是子串）
			if isWordBoundaryMatch(lower, deniedLower) {
				return true, "DENIED_CMD"
			}
		}
	}
	return false, ""
}

// isWordBoundaryMatch 检查 pattern 在 text 中是否是独立的词（前后是边界字符）。
func isWordBoundaryMatch(text, pattern string) bool {
	idx := strings.Index(text, pattern)
	if idx < 0 {
		return false
	}

	// 检查前面的字符
	if idx > 0 {
		prev := text[idx-1]
		if !isBoundaryChar(prev) {
			return false
		}
	}

	// 检查后面的字符
	endIdx := idx + len(pattern)
	if endIdx < len(text) {
		// 如果 pattern 以空格结尾，后面的字符可以是任意字符（空格本身就是边界）
		if pattern[len(pattern)-1] == ' ' {
			return true
		}
		next := text[endIdx]
		if !isBoundaryChar(next) {
			return false
		}
	}

	return true
}

// isBoundaryChar 判断字符是否是命令边界字符。
func isBoundaryChar(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '|' ||
		c == '&' || c == ';' || c == '(' || c == ')' ||
		c == '{' || c == '}' || c == '[' || c == ']' ||
		c == '<' || c == '>' || c == '"' || c == '\'' ||
		c == '`' || c == '$' || c == '#'
}

// accessesDeniedPath 检查命令是否访问了不允许的路径。
func (f *SafetyFilter) accessesDeniedPath(command string) (string, string) {
	lower := strings.ToLower(command)
	for _, path := range f.config.DeniedPaths {
		pathLower := strings.ToLower(path)
		if strings.Contains(lower, pathLower) {
			// 验证是路径（前后是空格、引号、行首/尾）
			if isPathBoundaryMatch(lower, pathLower) {
				return path, "DENIED_PATH"
			}
		}
	}
	return "", ""
}

// isPathBoundaryMatch 检查 path 在 text 中是否是独立的路径。
func isPathBoundaryMatch(text, path string) bool {
	idx := strings.Index(text, path)
	if idx < 0 {
		return false
	}

	// 路径通常以 / 开头或前面是空格/引号
	if idx > 0 {
		prev := text[idx-1]
		if prev != ' ' && prev != '\t' && prev != '"' && prev != '\'' &&
			prev != '=' && prev != ':' && prev != '/' {
			return false
		}
	}

	return true
}

// hasNetworkAccess 检查命令是否包含网络访问。
func (f *SafetyFilter) hasNetworkAccess(command string) string {
	urlPatterns := []string{"http://", "https://", "ftp://"}
	lower := strings.ToLower(command)
	for _, p := range urlPatterns {
		if idx := strings.Index(lower, p); idx >= 0 {
			urlStart := idx + len(p)
			end := strings.IndexAny(command[urlStart:], " /\"'\n\t")
			if end < 0 {
				end = len(command) - urlStart
			}
			domain := command[urlStart : urlStart+end]
			if colonIdx := strings.Index(domain, ":"); colonIdx > 0 {
				domain = domain[:colonIdx]
			}
			return domain
		}
	}
	return ""
}

// isDomainAllowed 检查域名是否在白名单中。
func (f *SafetyFilter) isDomainAllowed(domain string) bool {
	domain = strings.ToLower(domain)
	for _, allowed := range f.config.AllowedDomains {
		if domain == strings.ToLower(allowed) {
			return true
		}
		if strings.HasPrefix(allowed, "*.") {
			suffix := strings.ToLower(allowed[1:])
			if strings.HasSuffix(domain, suffix) {
				return true
			}
		}
	}
	return false
}

// hasShellInjection 检查命令是否包含可能的 shell 注入模式。
func (f *SafetyFilter) hasShellInjection(command string) string {
	dangerousPatterns := []struct {
		pattern string
		desc    string
	}{
		{"$(", "命令替换"},
		{"`", "反引号命令替换"},
		{"> /", "重定向到绝对路径"},
		{"| sh", "管道到 sh"},
		{"| bash", "管道到 bash"},
		{"eval ", "eval 命令"},
		{"exec ", "exec 命令"},
	}

	for _, dp := range dangerousPatterns {
		if strings.Contains(command, dp.pattern) {
			return dp.desc
		}
	}

	// || 和 && 只在 shell 命令上下文中才算注入
	// Go 代码里的 if a || b 不是注入
	// 但如果命令包含 shell 特有的结构，则标记
	if strings.Contains(command, "||") || strings.Contains(command, "&&") {
		// 检查是否是纯 shell 命令（包含 rm、curl 等危险命令）
		shellCmds := []string{"rm ", "curl ", "wget ", "sudo ", "chmod ", "chown "}
		lower := strings.ToLower(command)
		for _, sc := range shellCmds {
			if strings.Contains(lower, sc) {
				return "shell 命令链接"
			}
		}
	}

	return ""
}

// ========== 审计日志 ==========

// AuditEntry 审计日志条目。
type AuditEntry struct {
	Timestamp string   `json:"timestamp"`
	Command   string   `json:"command"`
	Decision  Decision `json:"decision"`
	RiskLevel string   `json:"risk_level"`
	RuleID    string   `json:"rule_id"`
	Reason    string   `json:"reason"`
}

// ToAuditEntry 将决策转换为审计日志条目。
func (d *SafetyDecision) ToAuditEntry() AuditEntry {
	return AuditEntry{
		Timestamp: time.Now().Format(time.RFC3339),
		Command:   d.Command,
		Decision:  d.Decision,
		RiskLevel: d.RiskLevel,
		RuleID:    d.RuleID,
		Reason:    d.Reason,
	}
}

// AuditLogger 审计日志写入器。
type AuditLogger struct {
	filePath string
}

// NewAuditLogger 创建审计日志写入器。
func NewAuditLogger(filePath string) *AuditLogger {
	return &AuditLogger{filePath: filePath}
}

// Log 记录一条审计日志。
func (l *AuditLogger) Log(decision *SafetyDecision) {
	entry := decision.ToAuditEntry()

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	// 追加写入 JSONL 格式（每行一条 JSON）
	f, err := os.OpenFile(l.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	f.Write(append(data, '\n'))
}
