//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"regexp"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

var sandboxLocationRE = regexp.MustCompile(`(?m)([A-Za-z0-9_./-]+\.go):([0-9]+)(?::[0-9]+)?:\s*(.+)`)
var staticcheckRuleRE = regexp.MustCompile(`\(([A-Z]+[0-9]+)\)`)

func ParseSandboxFindings(runs []SandboxRun) []Finding {
	var findings []Finding
	for _, run := range runs {
		if run.Status == "success" || run.Status == "skipped" {
			continue
		}
		text := strings.TrimSpace(run.Stdout + "\n" + run.Stderr)
		for _, match := range sandboxLocationRE.FindAllStringSubmatch(text, -1) {
			line, _ := strconv.Atoi(match[2])
			msg := strings.TrimSpace(match[3])
			findings = append(findings, Finding{
				Severity:       sandboxSeverity(run.Command, msg),
				Category:       sandboxCategory(run.Command),
				File:           strings.TrimPrefix(match[1], "work/repo/"),
				Line:           line,
				Title:          sandboxTitle(run.Command, msg),
				Evidence:       redactSecrets(match[0]),
				Recommendation: sandboxRecommendation(run.Command),
				Confidence:     0.82,
				Source:         "sandbox:" + sandboxRunKey(run),
				RuleID:         sandboxRuleID(run.Command, msg),
			})
		}
	}
	return DedupeFindings(findings)
}

func sandboxRunKey(run SandboxRun) string {
	return strings.TrimSpace(run.Command + " " + strings.Join(run.Args, " "))
}

func sandboxSeverity(cmd, msg string) Severity {
	if cmd == "staticcheck" && strings.Contains(msg, "SA") {
		return SeverityMedium
	}
	if cmd == "go" {
		return SeverityMedium
	}
	return SeverityLow
}

func sandboxCategory(cmd string) string {
	switch cmd {
	case "go":
		return "go_tooling"
	case "staticcheck":
		return "static_analysis"
	default:
		return "sandbox"
	}
}

func sandboxTitle(cmd, msg string) string {
	msg = strings.TrimSpace(msg)
	if len(msg) > 96 {
		msg = msg[:96] + "..."
	}
	switch cmd {
	case "staticcheck":
		return "Staticcheck reported an issue"
	case "go":
		return "Go tool reported an issue"
	default:
		return "Sandbox tool reported an issue"
	}
}

func sandboxRecommendation(cmd string) string {
	switch cmd {
	case "staticcheck":
		return "Fix the staticcheck diagnostic or document why the rule should be suppressed."
	case "go":
		return "Fix the Go tool diagnostic and rerun the sandbox checks."
	default:
		return "Inspect the sandbox diagnostic and rerun the review after fixing it."
	}
}

func sandboxRuleID(cmd, msg string) string {
	if cmd == "staticcheck" {
		if m := staticcheckRuleRE.FindStringSubmatch(msg); m != nil {
			return "sandbox/staticcheck/" + strings.ToLower(m[1])
		}
		return "sandbox/staticcheck/diagnostic"
	}
	if cmd == "go" {
		return "sandbox/go/diagnostic"
	}
	return "sandbox/tool/diagnostic"
}

func artifactSize(f codeexecutor.File) int64 {
	if f.SizeBytes > 0 {
		return f.SizeBytes
	}
	return int64(len(f.Content))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
