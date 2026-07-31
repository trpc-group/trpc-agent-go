//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var (
	sourceProcessPattern = regexp.MustCompile(
		`(?i)(subprocess\.|os\.system\s*\(|os\.popen\s*\(|exec\.command\s*\(|child_process|deno\.command|runtime\.exec\s*\()`,
	)
	sourceNetworkPattern = regexp.MustCompile(
		`(?i)(requests\.|urllib\.|httpx\.|aiohttp\.|http\.(?:get|post|newrequest)\s*\(|fetch\s*\(|axios\.|net\.dial\s*\(|urlopen\s*\(|["'](?:curl|wget|nc|netcat|ssh|scp|sftp)["'])`,
	)
	sourceShellExecutablePattern = regexp.MustCompile(
		`(?i)["'](?:sh|bash|zsh|dash|cmd(?:\.exe)?|powershell|pwsh)["']`,
	)
	sourceShellCommandFlagPattern = regexp.MustCompile(
		`(?i)["'](?:-c|/c|-(?:command|encodedcommand))["']`,
	)
	sourceDependencyPattern = regexp.MustCompile(
		`(?i)\b(?:go|npm|pnpm|yarn|bun|pip3?|apt(?:-get)?|apk|cargo|gem|brew)\s+(?:install|add)\b`,
	)
	sourceInfiniteLoopPattern = regexp.MustCompile(
		`(?im)(\bwhile\s*(?:\(\s*)?(?:true|1)(?:\s*\))?\s*[:{]|\bfor\s*\(\s*;\s*;\s*\)|^\s*for\s*\{)`,
	)
	sourceOutputFloodPattern = regexp.MustCompile(
		`(?i)(/dev/zero|\bwhile\s+(?:true|1).*\bprint\s*\(|\bfor\s*\(\s*;\s*;\s*\).*console\.log)`,
	)
	sourceDeletePattern = regexp.MustCompile(
		`(?i)(shutil\.rmtree\s*\(|fs\.(?:rm|rmdir)\s*\(|os\.remove\s*\(|removeall\s*\()`,
	)
	sourceURLPattern = regexp.MustCompile(`https?://[^\s'"` + "`" + `<>]+`)
)

func (s *Scanner) scanCodeBlock(
	ctx context.Context,
	index int,
	block CodeBlock,
	input ScanInput,
) ([]Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	language := strings.ToLower(strings.TrimSpace(block.Language))
	if strings.TrimSpace(block.Code) == "" {
		return []Finding{finding(
			DecisionDeny,
			RiskLevelHigh,
			RuleInvalidInput,
			fmt.Sprintf("code block %d is empty", index),
			"provide a non-empty bounded code block",
		)}, nil
	}
	switch language {
	case "bash", "sh", "shell":
		return s.scanShellScript(ctx, index, block.Code, input.WorkingDirectory)
	case "python", "py", "go", "golang", "javascript", "js", "node",
		"typescript", "ts":
		findings := s.scanSourceText(index, language, block.Code, input.WorkingDirectory)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return findings, nil
	default:
		return []Finding{finding(
			DecisionAsk,
			RiskLevelHigh,
			RuleUnknownLanguage,
			fmt.Sprintf("code block %d uses unsupported language %q", index, block.Language),
			"obtain human review or use a language with an explicit safety scanner",
		)}, nil
	}
}

func (s *Scanner) scanShellScript(
	ctx context.Context,
	blockIndex int,
	code string,
	workingDirectory string,
) ([]Finding, error) {
	lines := strings.Split(strings.ReplaceAll(code, "\r\n", "\n"), "\n")
	var findings []Finding
	for lineIndex, raw := range lines {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			if lineIndex == 0 && strings.HasPrefix(line, "#!") {
				continue
			}
			continue
		}
		lineFindings := s.scanCommandInput(ScanInput{
			ToolName:         fmt.Sprintf("code_block_%d", blockIndex),
			Backend:          BackendCodeExecutor,
			Command:          line,
			WorkingDirectory: workingDirectory,
		})
		for i := range lineFindings {
			lineFindings[i].Evidence = fmt.Sprintf(
				"code block %d line %d: %s",
				blockIndex,
				lineIndex+1,
				lineFindings[i].Evidence,
			)
		}
		findings = append(findings, lineFindings...)
	}
	return findings, nil
}

func (s *Scanner) scanSourceText(
	blockIndex int,
	language string,
	code string,
	workingDirectory string,
) []Finding {
	var findings []Finding
	if sourceShellExecutablePattern.MatchString(code) &&
		sourceShellCommandFlagPattern.MatchString(code) {
		findings = append(findings, finding(
			DecisionDeny,
			RiskLevelCritical,
			RuleShellBypass,
			fmt.Sprintf("code block %d launches a shell command wrapper", blockIndex),
			"use a literal executable and argv values without a nested command interpreter",
		))
	}
	if dangerousDeletePattern.MatchString(code) || sourceDeletePattern.MatchString(code) {
		findings = append(findings, finding(
			DecisionDeny,
			RiskLevelCritical,
			RuleDangerousDelete,
			fmt.Sprintf("code block %d contains a recursive or programmatic deletion primitive", blockIndex),
			"remove deletion from generated code or constrain it in a reviewed sandbox operation",
		))
	}
	if sourceInfiniteLoopPattern.MatchString(code) || sourceOutputFloodPattern.MatchString(code) {
		findings = append(findings, finding(
			DecisionDeny,
			RiskLevelCritical,
			RuleResourceAbuse,
			fmt.Sprintf("code block %d contains an unbounded loop or output source", blockIndex),
			"bound iterations and output, and enforce executor timeout and output limits",
		))
	}
	candidates := pathCandidates(code)
	findings = append(findings, scanSensitivePaths(candidates)...)
	findings = append(findings, s.scanConfiguredPaths(candidates, workingDirectory)...)
	findings = append(findings, s.scanSourceNetwork(blockIndex, code)...)
	if sourceDependencyPattern.MatchString(code) {
		findings = append(findings, finding(
			DecisionAsk,
			RiskLevelHigh,
			RuleDependencyChange,
			fmt.Sprintf("%s code block %d contains a dependency installation", language, blockIndex),
			"review package provenance, version pinning, and install hooks before approval",
		))
	}
	if sourceProcessPattern.MatchString(code) {
		findings = append(findings, finding(
			DecisionAsk,
			RiskLevelHigh,
			RuleToolMetadata,
			fmt.Sprintf("%s code block %d can launch a child process", language, blockIndex),
			"review the literal child command and execute only inside a constrained sandbox",
		))
	}
	return findings
}

func (s *Scanner) scanSourceNetwork(blockIndex int, code string) []Finding {
	if !sourceNetworkPattern.MatchString(code) {
		return nil
	}
	matches := sourceURLPattern.FindAllString(code, -1)
	if len(matches) == 0 {
		return []Finding{finding(
			DecisionDeny,
			RiskLevelCritical,
			RuleNetworkEgress,
			fmt.Sprintf("code block %d uses a network API with a non-literal destination", blockIndex),
			"use a literal http or https URL whose host is explicitly allowlisted",
		)}
	}
	for _, rawURL := range matches {
		parsed, err := url.Parse(strings.TrimRight(rawURL, ".,;:)\"'"))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Hostname() == "" || !s.domainAllowed(parsed.Hostname()) {
			host := rawURL
			if err == nil && parsed.Hostname() != "" {
				host = parsed.Hostname()
			}
			return []Finding{finding(
				DecisionDeny,
				RiskLevelCritical,
				RuleNetworkEgress,
				fmt.Sprintf("code block %d network host %q is not allowlisted", blockIndex, host),
				"use an allowlisted literal http or https destination",
			)}
		}
	}
	return nil
}
