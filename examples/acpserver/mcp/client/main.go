//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main runs a minimal ACP client for the stdio MCP example.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"

	acpsdk "github.com/coder/acp-go-sdk"
)

const (
	defaultPrompt = "Call the MCP tool named demo_greet. " +
		"Then answer with the exact greeting it returns."
	mcpServerName = "demo"
	greeting      = "hello from the ACP stdio MCP example"
)

type exampleClient struct{}

var _ acpsdk.Client = (*exampleClient)(nil)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	modelName := flag.String("model", "gpt-4o-mini", "OpenAI model name")
	prompt := flag.String("prompt", defaultPrompt, "Prompt sent to the ACP agent")
	flag.Parse()

	dir, err := exampleDir()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cmd := exec.CommandContext(ctx, "go", "run", "./server", "-model", *modelName)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create ACP server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("create ACP server stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return fmt.Errorf("start ACP server: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}()

	conn := acpsdk.NewClientSideConnection(&exampleClient{}, stdin, stdout)
	if _, err := conn.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
	}); err != nil {
		return fmt.Errorf("initialize ACP connection: %w", err)
	}

	fmt.Printf(
		"[client] session/new: creating session with stdio MCP server %q\n",
		mcpServerName,
	)
	session, err := conn.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd: dir,
		McpServers: []acpsdk.McpServer{{
			Stdio: &acpsdk.McpServerStdio{
				Name:    mcpServerName,
				Command: "go",
				Args:    []string{"run", "./mcpserver"},
				Env: []acpsdk.EnvVariable{{
					Name:  "ACP_EXAMPLE_MCP_GREETING",
					Value: greeting,
				}},
			},
		}},
	})
	if err != nil {
		return fmt.Errorf("create ACP session: %w", err)
	}
	fmt.Printf("session: %s\n", session.SessionId)

	fmt.Println("[client] session/prompt: sending prompt and waiting for agent response")
	response, err := conn.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: session.SessionId,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock(*prompt)},
	})
	if err != nil {
		return fmt.Errorf("send ACP prompt: %w", err)
	}
	fmt.Printf("\nstop reason: %s\n", response.StopReason)

	fmt.Println("[client] session/close: closing ACP session")
	if _, err := conn.CloseSession(ctx, acpsdk.CloseSessionRequest{
		SessionId: session.SessionId,
	}); err != nil {
		return fmt.Errorf("close ACP session: %w", err)
	}
	return nil
}

func exampleDir() (string, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("resolve example source path")
	}
	return filepath.Abs(filepath.Dir(filepath.Dir(filename)))
}

func (*exampleClient) RequestPermission(
	_ context.Context,
	request acpsdk.RequestPermissionRequest,
) (acpsdk.RequestPermissionResponse, error) {
	for _, option := range request.Options {
		if option.Kind == acpsdk.PermissionOptionKindAllowOnce {
			return selectedPermission(option.OptionId), nil
		}
	}
	return cancelledPermission(), nil
}

func (*exampleClient) SessionUpdate(
	_ context.Context,
	notification acpsdk.SessionNotification,
) error {
	update := notification.Update
	switch {
	case update.AgentMessageChunk != nil:
		text := update.AgentMessageChunk.Content.Text
		if text != nil {
			fmt.Print(text.Text)
		}
	case update.AgentThoughtChunk != nil:
		text := update.AgentThoughtChunk.Content.Text
		if text != nil {
			fmt.Printf("[thought] %s\n", text.Text)
		}
	case update.ToolCall != nil:
		fmt.Printf("\n[tool call] %s (%s)\n", update.ToolCall.Title, update.ToolCall.Status)
	case update.ToolCallUpdate != nil:
		status := toolCallStatus(update.ToolCallUpdate.Status)
		if status == "" {
			fmt.Printf("[tool update] %s\n", update.ToolCallUpdate.ToolCallId)
		} else {
			fmt.Printf("[tool update] %s (%s)\n", update.ToolCallUpdate.ToolCallId, status)
		}
	}
	return nil
}

func toolCallStatus(status *acpsdk.ToolCallStatus) string {
	if status == nil {
		return ""
	}
	return string(*status)
}

func (*exampleClient) ReadTextFile(
	context.Context,
	acpsdk.ReadTextFileRequest,
) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, errors.New("client file reads are not enabled")
}

func (*exampleClient) WriteTextFile(
	context.Context,
	acpsdk.WriteTextFileRequest,
) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, errors.New("client file writes are not enabled")
}

func (*exampleClient) CreateTerminal(
	context.Context,
	acpsdk.CreateTerminalRequest,
) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, errors.New("terminal support is not enabled")
}

func (*exampleClient) KillTerminal(
	context.Context,
	acpsdk.KillTerminalRequest,
) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, errors.New("terminal support is not enabled")
}

func (*exampleClient) TerminalOutput(
	context.Context,
	acpsdk.TerminalOutputRequest,
) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, errors.New("terminal support is not enabled")
}

func (*exampleClient) ReleaseTerminal(
	context.Context,
	acpsdk.ReleaseTerminalRequest,
) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, errors.New("terminal support is not enabled")
}

func (*exampleClient) WaitForTerminalExit(
	context.Context,
	acpsdk.WaitForTerminalExitRequest,
) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, errors.New("terminal support is not enabled")
}

func selectedPermission(optionID acpsdk.PermissionOptionId) acpsdk.RequestPermissionResponse {
	return acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.RequestPermissionOutcome{
			Selected: &acpsdk.RequestPermissionOutcomeSelected{OptionId: optionID},
		},
	}
}

func cancelledPermission() acpsdk.RequestPermissionResponse {
	return acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.RequestPermissionOutcome{
			Cancelled: &acpsdk.RequestPermissionOutcomeCancelled{},
		},
	}
}
