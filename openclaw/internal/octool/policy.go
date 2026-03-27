//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package octool

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

const (
	errCommandPolicyRejected = "exec_command blocked by safety " +
		"policy: %s"

	reasonSensitiveEnv = "reading or printing sensitive " +
		"credentials is not allowed in chat"
	reasonSensitivePath = "reading or modifying shell or " +
		"credential files is not allowed in chat"

	sensitivePathBoundaryChars = " \t\r\n\"'`=:/\\|&;()[]{}<>"
)

var (
	sensitiveEnvNamePattern = regexp.MustCompile(
		`(?i)\b[A-Z0-9_]*(TOKEN|SECRET|PASSWORD|PASSWD|API_KEY|` +
			`ACCESS_KEY|PRIVATE_KEY)[A-Z0-9_]*\b`,
	)

	sensitiveEnvRuntimeReadPatterns = []*regexp.Regexp{
		regexp.MustCompile(
			`(?i)\bos\.environ\.get\s*\(\s*["']([a-z0-9_]+)["']`,
		),
		regexp.MustCompile(
			`(?i)\bos\.environ\s*\[\s*["']([a-z0-9_]+)["']`,
		),
		regexp.MustCompile(
			`(?i)\b(?:os\.)?getenv\s*\(\s*["']([a-z0-9_]+)["']`,
		),
		regexp.MustCompile(
			`(?i)\b(?:os\.)?lookupenv\s*\(\s*["']([a-z0-9_]+)["']`,
		),
		regexp.MustCompile(`(?i)\bprocess\.env\.([a-z0-9_]+)\b`),
		regexp.MustCompile(
			`(?i)\bprocess\.env\s*\[\s*["']([a-z0-9_]+)["']`,
		),
		regexp.MustCompile(`(?i)\benv\s*\[\s*["']([a-z0-9_]+)["']`),
	}

	sensitivePathFragments = []string{
		".aws/config",
		".aws/credentials",
		".bash_profile",
		".bashrc",
		".config/gcloud",
		".docker/config.json",
		".env",
		".git-credentials",
		".kube/config",
		".netrc",
		".npmrc",
		".profile",
		".pypirc",
		".ssh/",
		".zprofile",
		".zshenv",
		".zshrc",
	}

	sensitiveEnvReadHints = []string{
		"${",
		"$",
		"printenv",
		"env ",
		"export ",
	}
)

// CommandRequest is the normalized command metadata checked by policies.
type CommandRequest struct {
	Command    string
	Workdir    string
	Env        map[string]string
	Pty        bool
	Background bool
	YieldMs    *int
	TimeoutS   *int
}

// CommandPolicy decides whether one exec_command call is allowed.
type CommandPolicy func(context.Context, CommandRequest) error

// NewChatCommandSafetyPolicy blocks credential exfiltration attempts and
// direct access to shell/profile credential files in chat contexts.
func NewChatCommandSafetyPolicy() CommandPolicy {
	return func(
		_ context.Context,
		req CommandRequest,
	) error {
		command := strings.ToLower(strings.TrimSpace(req.Command))
		if command == "" {
			return nil
		}
		if blocksSensitivePath(command) {
			return fmt.Errorf(
				errCommandPolicyRejected,
				reasonSensitivePath,
			)
		}
		if blocksSensitiveEnv(command) {
			return fmt.Errorf(
				errCommandPolicyRejected,
				reasonSensitiveEnv,
			)
		}
		return nil
	}
}

func newCommandRequest(params execParams) CommandRequest {
	return CommandRequest{
		Command:    strings.TrimSpace(params.Command),
		Workdir:    strings.TrimSpace(params.Workdir),
		Env:        params.Env,
		Pty:        params.Pty,
		Background: params.Background,
		YieldMs:    params.YieldMs,
		TimeoutS:   params.TimeoutS,
	}
}

func blocksSensitiveEnv(command string) bool {
	for _, pattern := range sensitiveEnvRuntimeReadPatterns {
		matches := pattern.FindAllStringSubmatch(command, -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			if sensitiveEnvNamePattern.MatchString(match[1]) {
				return true
			}
		}
	}
	if !sensitiveEnvNamePattern.MatchString(command) {
		return false
	}
	for _, hint := range sensitiveEnvReadHints {
		if strings.Contains(command, hint) {
			return true
		}
	}
	return false
}

func blocksSensitivePath(command string) bool {
	for _, fragment := range sensitivePathFragments {
		if containsSensitivePathFragment(command, fragment) {
			return true
		}
	}
	return false
}

func containsSensitivePathFragment(command, fragment string) bool {
	if fragment == "" {
		return false
	}
	offset := 0
	for offset < len(command) {
		idx := strings.Index(command[offset:], fragment)
		if idx < 0 {
			return false
		}
		idx += offset
		if hasSensitivePathBoundaryBefore(command, idx) &&
			hasSensitivePathBoundaryAfter(command, idx+len(fragment), fragment) {
			return true
		}
		offset = idx + 1
	}
	return false
}

func hasSensitivePathBoundaryBefore(command string, idx int) bool {
	if idx <= 0 {
		return true
	}
	return isSensitivePathBoundary(command[idx-1])
}

func hasSensitivePathBoundaryAfter(
	command string,
	idx int,
	fragment string,
) bool {
	if strings.HasSuffix(fragment, "/") {
		return true
	}
	if idx >= len(command) {
		return true
	}
	if fragment == ".env" && command[idx] == '.' {
		return true
	}
	return isSensitivePathBoundary(command[idx])
}

func isSensitivePathBoundary(ch byte) bool {
	return strings.ContainsRune(sensitivePathBoundaryChars, rune(ch))
}
