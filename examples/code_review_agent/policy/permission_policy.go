//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package policy

import (
	"path/filepath"
	"regexp"
	"strings"
)

type PermissionAction string

const (
	ActionAllow  PermissionAction = "ALLOW"
	ActionDeny   PermissionAction = "DENY"
	ActionReview PermissionAction = "REVIEW"
)

type PermissionResult struct {
	Action  PermissionAction
	Reason  string
	Command string
}

type PermissionPolicy struct {
	allowedCommands  []string
	deniedCommands   []string
	highRiskPatterns []*regexp.Regexp
}

var highRiskPatternList = []string{
	`rm\s+-rf`,
	`rm\s+-r`,
	`rm\s+--recursive`,
	`chmod\s+-R`,
	`chown\s+-R`,
	`curl\s+-X\s+DELETE`,
	`curl\s+-X\s+POST`,
	`curl\s+-d`,
	`wget\s+.*\|.*sh`,
	`bash\s+-c\s+.*\|.*`,
	`python\s+-c\s+.*subprocess`,
	`python\s+-c\s+.*os\.system`,
	`\$\(.*\)`,
	"`.*`",
	`>>\s+/etc/`,
	`>>\s+/usr/`,
	`>>\s+/var/`,
	`chmod\s+777`,
	`chmod\s+0777`,
	`su\s+-`,
	`sudo\s+`,
	`docker\s+run`,
	`kubectl\s+apply`,
	`ssh\s+-i`,
	`git\s+push`,
}

func NewPermissionPolicy() *PermissionPolicy {
	policy := &PermissionPolicy{
		allowedCommands: []string{
			"go",
			"go vet",
			"go test",
			"go build",
			"staticcheck",
			"bash",
			"cat",
			"grep",
			"awk",
			"sed",
			"head",
			"tail",
			"wc",
			"mkdir",
			"cp",
		},
		deniedCommands: []string{
			"rm",
			"rmdir",
			"mv",
			"chmod",
			"chown",
			"rm -rf",
			"shutdown",
			"reboot",
			"kill",
			"killall",
			"poweroff",
			"halt",
			"su",
			"sudo",
			"passwd",
			"rm -rf /",
		},
	}

	policy.highRiskPatterns = make([]*regexp.Regexp, len(highRiskPatternList))
	for i, pattern := range highRiskPatternList {
		policy.highRiskPatterns[i] = regexp.MustCompile(`(?i)` + pattern)
	}

	return policy
}

func (p *PermissionPolicy) CheckCommand(command string) PermissionResult {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return PermissionResult{
			Action:  ActionReview,
			Reason:  "Empty command",
			Command: command,
		}
	}

	lowerCmd := strings.ToLower(cmd)
	cmdArgs := parseCommand(cmd)
	executable := strings.ToLower(cmdArgs[0])
	executableName := strings.ToLower(filepath.Base(cmdArgs[0]))

	for _, denied := range p.deniedCommands {
		deniedLow := strings.ToLower(denied)
		if executable == deniedLow || executableName == deniedLow || strings.HasPrefix(lowerCmd, deniedLow+" ") {
			return PermissionResult{
				Action:  ActionDeny,
				Reason:  "Command is denied",
				Command: cmd,
			}
		}
	}

	for _, pattern := range p.highRiskPatterns {
		if pattern.MatchString(lowerCmd) {
			return PermissionResult{
				Action:  ActionReview,
				Reason:  "High-risk command pattern detected",
				Command: cmd,
			}
		}
	}

	for _, allowed := range p.allowedCommands {
		allowedLow := strings.ToLower(allowed)
		if executable == allowedLow || executableName == allowedLow || strings.HasPrefix(lowerCmd, allowedLow+" ") {
			return PermissionResult{
				Action:  ActionAllow,
				Reason:  "Command is allowed",
				Command: cmd,
			}
		}
	}

	return PermissionResult{
		Action:  ActionReview,
		Reason:  "Command not in allow list; requires manual review",
		Command: cmd,
	}
}

func parseCommand(cmd string) []string {
	args := strings.Fields(cmd)
	if len(args) == 0 {
		return []string{cmd}
	}
	return args
}

func (p *PermissionPolicy) IsAllowed(command string) bool {
	result := p.CheckCommand(command)
	return result.Action == ActionAllow
}

func (p *PermissionPolicy) IsDenied(command string) bool {
	result := p.CheckCommand(command)
	return result.Action == ActionDeny
}

func (p *PermissionPolicy) NeedsReview(command string) bool {
	result := p.CheckCommand(command)
	return result.Action == ActionReview
}
