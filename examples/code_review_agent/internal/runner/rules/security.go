//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package rules provides built-in code review rule implementations.
package rules

import (
	"context"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/runner"
)

var (
	// SQL injection patterns: string concatenation in SQL queries.
	sqlInjectionPattern = regexp.MustCompile(`(?i)(SELECT|INSERT|UPDATE|DELETE|WHERE).*` +
		`[\+\` + "`" + `]` + `\s*(request\.|params\[|userInput|input\.|req\.)`)

	// Command injection patterns: os/exec with string concatenation.
	cmdInjectionPattern = regexp.MustCompile(`(?i)(exec\.Command|exec\.CommandContext|syscall\.Exec)` +
		`\s*\(\s*.*[\+\` + "`" + `]`)

	// Hardcoded credential patterns.
	hardcodedKeyPattern = regexp.MustCompile(`(?i)(api[_-]?key|apisecret|secretkey|password|passwd)` +
		`\s*[:=]\s*['\"][a-zA-Z0-9_\-]{16,}`)

	// Direct use of os/exec with strings that look like user-controlled vars.
	execWithConcatPattern = regexp.MustCompile(`exec\.Command\(\s*"sh"\s*,\s*"-c"\s*,`)
)

// SecurityRule checks for security issues: SQL injection, command injection, hardcoded keys.
type SecurityRule struct {
	runner.RuleBase
}

// NewSecurityRule creates a new security rule.
func NewSecurityRule() *SecurityRule {
	return &SecurityRule{
		RuleBase: runner.RuleBase{
			IDValue:       "GO_SECURITY_INJECTION",
			CategoryValue: finding.CategorySecurity,
			DefaultSev:    finding.SeverityCritical,
		},
	}
}

// Check examines file content for security issues.
func (r *SecurityRule) Check(ctx context.Context, file finding.ChangedFileInfo, content string) ([]finding.Finding, error) {
	if !strings.HasSuffix(file.File, ".go") {
		return nil, nil
	}

	var findings []finding.Finding

	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lineNum := i + 1

		// Check SQL injection.
		if sqlInjectionPattern.MatchString(line) {
			findings = append(findings, runner.NewFinding(
				&r.RuleBase, file.File, lineNum,
				"Potential SQL injection: string concatenation in SQL query",
				line,
				"Use parameterized queries or prepared statements instead of string concatenation",
				finding.ConfidenceHigh,
			))
		}

		// Check command injection.
		if cmdInjectionPattern.MatchString(line) {
			findings = append(findings, runner.NewFinding(
				&r.RuleBase, file.File, lineNum,
				"Potential command injection: user input in shell command",
				line,
				"Use exec.Command with separate arguments, avoid shell injection vectors",
				finding.ConfidenceHigh,
			))
		}

		// Check os/exec -c with suspicious args.
		if execWithConcatPattern.MatchString(line) {
			findings = append(findings, runner.NewFinding(
				&r.RuleBase, file.File, lineNum,
				"Potential command injection: shell -c with concatenated arguments",
				line,
				"Avoid 'sh -c' with exec.Command; pass arguments separately",
				finding.ConfidenceHigh,
			))
		}

		// Check hardcoded credentials.
		if hardcodedKeyPattern.MatchString(line) {
			findings = append(findings, runner.NewFinding(
				&r.RuleBase, file.File, lineNum,
				"Hardcoded credential detected",
				line,
				"Move secrets to environment variables or a secret management system",
				finding.ConfidenceMedium,
			))
		}
	}

	return findings, nil
}

// HardcodedKeyRule detects hardcoded API keys, tokens, and private keys in source files.
type HardcodedKeyRule struct {
	runner.RuleBase
}

// NewHardcodedKeyRule creates a new hardcoded key rule.
func NewHardcodedKeyRule() *HardcodedKeyRule {
	return &HardcodedKeyRule{
		RuleBase: runner.RuleBase{
			IDValue:       "GO_SECURITY_HARDCODED_KEY",
			CategoryValue: finding.CategorySensitiveInfo,
			DefaultSev:    finding.SeverityCritical,
		},
	}
}

// Check examines file content for hardcoded API keys and tokens.
func (r *HardcodedKeyRule) Check(ctx context.Context, file finding.ChangedFileInfo, content string) ([]finding.Finding, error) {
	var findings []finding.Finding

	lines := strings.Split(content, "\n")
	for i, line := range lines {
		// Check for sk-xxx (OpenAI keys), ghp_xxx (GitHub tokens), etc.
		if matched := detectInlineSecret(line); matched != "" {
			findings = append(findings, runner.NewFinding(
				&r.RuleBase, file.File, i+1,
				"Hardcoded secret/token detected",
				matched,
				"Use environment variables or a vault service for secrets",
				finding.ConfidenceHigh,
			))
		}
	}
	return findings, nil
}

var (
	openAIKeyPattern    = regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`)
	gitHubTokenPattern  = regexp.MustCompile(`gh[ps]_[A-Za-z0-9]{36}`)
	genericTokenPattern = regexp.MustCompile(`['\"][A-Za-z0-9_\-]{40,}['"]`)
	privateKeyPattern   = regexp.MustCompile(`-----BEGIN (RSA |EC )?PRIVATE KEY-----`)
)

func detectInlineSecret(line string) string {
	switch {
	case openAIKeyPattern.MatchString(line):
		return openAIKeyPattern.FindString(line)
	case gitHubTokenPattern.MatchString(line):
		return gitHubTokenPattern.FindString(line)
	case privateKeyPattern.MatchString(line):
		return privateKeyPattern.FindString(line)
	}
	return ""
}
