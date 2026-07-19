//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewer

import "io"

// Config contains model selection and sandbox settings for one reviewer instance.
type Config struct {
	Mode     string
	Model    ModelConfig
	Sandbox  SandboxConfig
	Approval ApprovalConfig
}

// ModelConfig identifies the DeepSeek-compatible model endpoint used when the
// fake-model mode is not selected.
type ModelConfig struct {
	Name    string
	BaseURL string
	APIKey  string
}

// SandboxConfig selects the workspace execution backend.
type SandboxConfig struct {
	Backend string
}

// ApprovalConfig supplies the interactive terminal used to approve governed
// code execution. Fake-model runs use deterministic approval and do not read
// Input.
type ApprovalConfig struct {
	Input  io.Reader
	Output io.Writer
}
