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
	"path/filepath"
	"strings"
)

const (
	errCommandPolicyRejected = "exec_command blocked by safety " +
		"policy: %s"

	reasonSensitivePath = "reading or modifying shell or " +
		"credential files is not allowed in chat"

	sensitivePathBoundaryChars = " \t\r\n\"'`=:/\\|&;()[]{}<>"

	envTRPCClawEnvFile  = "TRPC_CLAW_ENV_FILE"
	envTRPCClawStateDir = "TRPC_CLAW_STATE_DIR"

	protectedRuntimeEnvRelPath = "runtime/env.sh"
	protectedGitCredentialFile = "git-credentials"

	shellQuoteChars = `"'`
)

var protectedPathFragments = []string{
	".aws/credentials",
	".bash_profile",
	".bashrc",
	".config/gcloud",
	".docker/config.json",
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

// NewChatCommandSafetyPolicy blocks direct access to protected shell and
// credential paths in chat contexts.
func NewChatCommandSafetyPolicy() CommandPolicy {
	return func(
		_ context.Context,
		req CommandRequest,
	) error {
		if blocksSensitivePathRequest(req) {
			return fmt.Errorf(
				errCommandPolicyRejected,
				reasonSensitivePath,
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

func blocksSensitivePath(command string) bool {
	return matchesProtectedPathFragments(
		normalizePolicyCommand(command),
		protectedPathFragments,
	)
}

func blocksSensitivePathRequest(req CommandRequest) bool {
	workdirFragments := dynamicProtectedWorkdirFragments(req.Env)
	if blocksSensitivePathValue(
		req.Workdir,
		protectedWorkdirFragments(),
		workdirFragments,
		req.Env,
	) {
		return true
	}

	return blocksSensitivePathValue(
		req.Command,
		protectedPathFragments,
		dynamicProtectedPathFragments(req.Env),
		req.Env,
	)
}

func blocksSensitivePathValue(
	raw string,
	protectedFragments []string,
	dynamicFragments []string,
	env map[string]string,
) bool {
	value := normalizePolicyCommand(raw)
	if value == "" {
		return false
	}
	if matchesProtectedPathFragments(value, protectedFragments) {
		return true
	}
	if matchesProtectedPathFragments(value, dynamicFragments) {
		return true
	}
	return matchesProtectedPathFragments(
		expandProtectedEnvReferences(value, env),
		dynamicFragments,
	)
}

func normalizePolicyCommand(command string) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return ""
	}
	return strings.ToLower(filepath.ToSlash(trimmed))
}

func blocksSensitivePathWithFragments(
	command string,
	fragments []string,
) bool {
	if command == "" {
		return false
	}
	for _, fragment := range fragments {
		if containsSensitivePathFragment(command, fragment) {
			return true
		}
	}
	return false
}

func matchesProtectedPathFragments(
	command string,
	fragments []string,
) bool {
	if blocksSensitivePathWithFragments(command, fragments) {
		return true
	}
	unquoted := stripShellQuotes(command)
	if unquoted == command {
		return false
	}
	return blocksSensitivePathWithFragments(unquoted, fragments)
}

func dynamicProtectedPathFragments(env map[string]string) []string {
	out := make([]string, 0, 3)
	if len(env) == 0 {
		return out
	}
	out = appendProtectedPathFragment(
		out,
		env[envTRPCClawEnvFile],
	)

	stateDir := strings.TrimSpace(env[envTRPCClawStateDir])
	if stateDir == "" {
		return out
	}
	out = appendProtectedPathFragment(
		out,
		filepath.Join(stateDir, protectedRuntimeEnvRelPath),
	)
	out = appendProtectedPathFragment(
		out,
		filepath.Join(stateDir, protectedGitCredentialFile),
	)
	return out
}

func protectedWorkdirFragments() []string {
	out := make([]string, 0, len(protectedPathFragments))
	for _, fragment := range protectedPathFragments {
		out = append(
			out,
			strings.TrimSuffix(fragment, "/"),
		)
	}
	return out
}

func dynamicProtectedWorkdirFragments(env map[string]string) []string {
	out := make([]string, 0, 3)
	if len(env) == 0 {
		return out
	}
	out = appendProtectedPathDir(
		out,
		env[envTRPCClawEnvFile],
	)

	stateDir := strings.TrimSpace(env[envTRPCClawStateDir])
	if stateDir == "" {
		return out
	}
	out = appendProtectedPathFragment(out, stateDir)
	out = appendProtectedPathFragment(
		out,
		filepath.Join(
			stateDir,
			filepath.Dir(protectedRuntimeEnvRelPath),
		),
	)
	return out
}

func expandProtectedEnvReferences(
	command string,
	env map[string]string,
) string {
	if command == "" || len(env) == 0 {
		return command
	}
	expanded := expandProtectedEnvReference(
		command,
		envTRPCClawEnvFile,
		env[envTRPCClawEnvFile],
	)
	return expandProtectedEnvReference(
		expanded,
		envTRPCClawStateDir,
		env[envTRPCClawStateDir],
	)
}

func expandProtectedEnvReference(
	command string,
	envName string,
	value string,
) string {
	fragment := normalizePathFragment(value)
	if fragment == "" {
		return command
	}
	lowerName := strings.ToLower(envName)
	replacer := strings.NewReplacer(
		"$"+lowerName,
		fragment,
		"${"+lowerName+"}",
		fragment,
	)
	return replacer.Replace(command)
}

func appendProtectedPathFragment(out []string, raw string) []string {
	fragment := normalizePathFragment(raw)
	if fragment == "" {
		return out
	}
	return append(out, fragment)
}

func appendProtectedPathDir(out []string, raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return out
	}
	dir := filepath.Dir(trimmed)
	if dir == "." {
		return out
	}
	return appendProtectedPathFragment(out, dir)
}

func normalizePathFragment(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return strings.ToLower(filepath.ToSlash(trimmed))
}

func stripShellQuotes(command string) string {
	if command == "" {
		return ""
	}
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(shellQuoteChars, r) {
			return -1
		}
		return r
	}, command)
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
