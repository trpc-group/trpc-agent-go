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
)

var (
	sensitiveEnvNamePattern = regexp.MustCompile(
		`(?i)\b[A-Z0-9_]*(TOKEN|SECRET|PASSWORD|PASSWD|API_KEY|` +
			`ACCESS_KEY|PRIVATE_KEY)[A-Z0-9_]*\b`,
	)

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
		if strings.Contains(command, fragment) {
			return true
		}
	}
	return false
}
