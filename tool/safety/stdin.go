// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

func (s *Scanner) scanStdin(req ExecutionRequest) []Finding {
	pipe, err := shellsafe.Parse(req.Command)
	if err != nil {
		return nil
	}
	var findings []Finding
	for i, argv := range pipe.Commands {
		if !curlReadsConfigFromStdin(argv) {
			continue
		}
		loc := fmt.Sprintf("stdin.segment[%d]", i)
		configArgv, err := parseCurlStdinConfig(req.Stdin)
		if err != nil {
			findings = append(findings, finding(
				RuleShellParseUnsafe, CategoryShellBypass, RiskHigh, s.policy.ParseErrorAction,
				"curl stdin config could not be parsed safely: "+err.Error(),
				loc,
				"Use explicit curl arguments or a simple, reviewable stdin config.",
			))
			continue
		}
		findings = append(findings, s.scanArgvInCwd(configArgv, loc, req.Cwd)...)
	}
	return findings
}

func curlReadsConfigFromStdin(argv []string) bool {
	if len(argv) < 2 || normalizeCommandName(argv[0]) != "curl" {
		return false
	}
	for i := 1; i < len(argv); i++ {
		arg := strings.ToLower(strings.TrimSpace(argv[i]))
		if arg == "--config=-" || arg == "-k-" {
			return true
		}
		if (arg == "--config" || arg == "-k") && i+1 < len(argv) && argv[i+1] == "-" {
			return true
		}
	}
	return false
}

func parseCurlStdinConfig(input string) ([]string, error) {
	argv := []string{"curl"}
	for lineNo, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := splitCurlConfigLine(line)
		if !ok {
			return nil, fmt.Errorf("line %d is malformed", lineNo+1)
		}
		name = strings.TrimLeft(strings.TrimSpace(name), "-")
		if name == "" || strings.EqualFold(name, "config") {
			return nil, fmt.Errorf("line %d uses an unsupported option", lineNo+1)
		}
		argv = append(argv, "--"+name)
		if value != "" {
			argv = append(argv, strings.Trim(strings.TrimSpace(value), `"'`))
		}
	}
	if len(argv) == 1 {
		return nil, fmt.Errorf("config is empty")
	}
	return argv, nil
}

func splitCurlConfigLine(line string) (string, string, bool) {
	if name, value, ok := strings.Cut(line, "="); ok {
		return name, value, strings.TrimSpace(name) != ""
	}
	parts := strings.Fields(line)
	if len(parts) == 1 {
		return parts[0], "", true
	}
	if len(parts) == 2 {
		return parts[0], parts[1], true
	}
	return "", "", false
}
