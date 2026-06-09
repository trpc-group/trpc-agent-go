//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package event

// LatencyDiagnosticExtensionKey stores pre-LLM latency diagnostics on events.
const LatencyDiagnosticExtensionKey = "trpc_agent.latency_diagnostic"

// LatencyDiagnostic carries non-sensitive timing context for preprocessing.
type LatencyDiagnostic struct {
	Stage         string `json:"stage,omitempty"`
	Status        string `json:"status,omitempty"`
	Summary       string `json:"summary,omitempty"`
	TokenCount    int    `json:"token_count,omitempty"`
	Threshold     int    `json:"threshold,omitempty"`
	ContextWindow int    `json:"context_window,omitempty"`
	MessageCount  int    `json:"message_count,omitempty"`
	ToolCount     int    `json:"tool_count,omitempty"`
	FilterKey     string `json:"filter_key,omitempty"`
	Updated       *bool  `json:"updated,omitempty"`
}
