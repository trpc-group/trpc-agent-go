//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// claudeAgentConfig configures how the Claude CLI is invoked and where artifacts are saved.
type claudeAgentConfig struct {
	ClaudeBin     string
	SaveClaudeLog bool
	OutputDir     string
}

// claudeAgent executes the Claude CLI and maps its output back into evaluation events.
type claudeAgent struct {
	name        string
	description string
	cfg         claudeAgentConfig
}

// newClaudeAgent validates the configuration and constructs a new Claude CLI agent.
func newClaudeAgent(cfg claudeAgentConfig) (*claudeAgent, error) {
	if cfg.ClaudeBin == "" {
		return nil, fmt.Errorf("claude bin is empty")
	}
	return &claudeAgent{
		name:        "claude-cli-agent",
		description: "Runs Claude CLI and returns the full stdout/stderr for evaluation.",
		cfg:         cfg,
	}, nil
}

// Info implements agent.Agent.
func (a *claudeAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: a.description,
	}
}

// Tools implements agent.Agent.
func (a *claudeAgent) Tools() []tool.Tool { return nil }

// SubAgents implements agent.Agent.
func (a *claudeAgent) SubAgents() []agent.Agent { return nil }

// FindSubAgent implements agent.Agent.
func (a *claudeAgent) FindSubAgent(string) agent.Agent { return nil }

// Run implements agent.Agent.
func (a *claudeAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	out := make(chan *event.Event)
	go func() {
		defer close(out)

		if invocation == nil {
			return
		}
		if invocation.AgentName == "" {
			invocation.AgentName = a.name
		}
		if invocation.Agent == nil {
			invocation.Agent = a
		}

		prompt := strings.TrimSpace(invocation.Message.Content)
		rawOutput, err := a.runClaude(ctx, invocation.InvocationID, prompt)
		if err != nil {
			rawOutput = fmt.Sprintf("claude agent failed: %v", err)
		}

		emitClaudeToolEvents(ctx, invocation, out, a.name, rawOutput)

		rsp := &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: rawOutput,
					},
				},
			},
		}
		agent.EmitEvent(ctx, invocation, out, event.NewResponseEvent(invocation.InvocationID, a.name, rsp))
	}()
	return out, nil
}

// runClaude executes the CLI with a prompt and returns the raw combined stdout/stderr content.
func (a *claudeAgent) runClaude(ctx context.Context, invocationID, prompt string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("prompt is empty")
	}

	sseURL, shutdownMCP, err := startLocalMCPSSEServer(ctx)
	if err != nil {
		return "", fmt.Errorf("start mcp server: %w", err)
	}
	defer shutdownMCP()

	_, _ = a.execClaude(context.Background(), "mcp", "remove", "-s", "local", claudeMCPServerName)
	if _, err := a.execClaude(ctx, "mcp", "add", "-s", "local", "--transport", "sse", claudeMCPServerName, sseURL); err != nil {
		return "", fmt.Errorf("register mcp server: %w", err)
	}
	defer func() {
		_, _ = a.execClaude(context.Background(), "mcp", "remove", "-s", "local", claudeMCPServerName)
	}()

	args := []string{
		"-p",
	}
	args = append(args, prompt)
	args = append(args, "--verbose", "--output-format", "json")
	args = append(args, "--allowedTools")
	args = append(args, claudeCalculatorMCPToolName)
	cmd := exec.CommandContext(ctx, a.cfg.ClaudeBin, args...)
	cmd.Env = os.Environ()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	combined := append(stdout.Bytes(), stderr.Bytes()...)

	if a.cfg.SaveClaudeLog && a.cfg.OutputDir != "" {
		if err := os.MkdirAll(filepath.Join(a.cfg.OutputDir, "claude-cli-logs"), 0o755); err == nil {
			logPath := filepath.Join(a.cfg.OutputDir, "claude-cli-logs", fmt.Sprintf("%s.log.txt", invocationID))
			_ = os.WriteFile(logPath, combined, 0o644)
		}
	}

	if runErr != nil {
		return "", fmt.Errorf("claude exec failed: %w\n%s", runErr, strings.TrimSpace(string(combined)))
	}
	content := strings.TrimSpace(string(combined))
	if content == "" {
		return "", fmt.Errorf("claude exec succeeded but output is empty")
	}
	return content, nil
}

// execClaude runs a Claude CLI subcommand and returns its combined stdout/stderr output.
func (a *claudeAgent) execClaude(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, a.cfg.ClaudeBin, args...)
	cmd.Env = os.Environ()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	combined := append(stdout.Bytes(), stderr.Bytes()...)
	if runErr != nil {
		return "", fmt.Errorf("claude %s failed: %w\n%s", strings.Join(args, " "), runErr, strings.TrimSpace(string(combined)))
	}
	return strings.TrimSpace(string(combined)), nil
}
