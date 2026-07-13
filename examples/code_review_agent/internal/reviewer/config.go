//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewer

// Config contains model and sandbox settings for one reviewer instance.
type Config struct {
	Model   ModelConfig
	Sandbox SandboxConfig
}

// ModelConfig identifies the DeepSeek-compatible model endpoint.
type ModelConfig struct {
	Name    string
	BaseURL string
	APIKey  string
}

// SandboxConfig selects the workspace execution backend.
type SandboxConfig struct {
	Backend string
}
