//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main runs a minimal ACP client with client-side file tools.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"
)

const (
	inputFilename  = "input.txt"
	outputFilename = "copied.txt"
	inputContent   = "Hello from the ACP client files example.\n" +
		"This file is copied through fs/read_text_file and fs/write_text_file.\n"
	// JSON escaping can expand control bytes by six times. A 1 MiB response
	// limit leaves ample room below the ACP SDK's 10 MiB frame limit.
	maxReadTextFileBytes = 1 << 20
	readTextFileBuffer   = 32 << 10

	defaultPrompt = "Read input.txt from the session workspace and write its exact " +
		"content to copied.txt. Use the ACP client file tools. Do not ask for " +
		"confirmation before writing."
)

type exampleClient struct {
	workspace string
}

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
	workspaceFlag := flag.String(
		"workspace",
		"",
		"workspace directory; a temporary directory is used when empty",
	)
	flag.Parse()

	dir, err := exampleDir()
	if err != nil {
		return err
	}
	workspace, cleanup, err := prepareWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := os.WriteFile(
		filepath.Join(workspace, inputFilename),
		[]byte(inputContent),
		0o644,
	); err != nil {
		return fmt.Errorf("write example input: %w", err)
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

	client := &exampleClient{workspace: workspace}
	conn := acpsdk.NewClientSideConnection(client, stdin, stdout)
	if _, err := conn.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientCapabilities: acpsdk.ClientCapabilities{
			Fs: acpsdk.FileSystemCapabilities{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
		},
	}); err != nil {
		return fmt.Errorf("initialize ACP connection: %w", err)
	}

	fmt.Printf("[client] session/new: creating session in workspace %s\n", workspace)
	session, err := conn.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        workspace,
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		return fmt.Errorf("create ACP session: %w", err)
	}
	fmt.Printf("session: %s\n", session.SessionId)
	fmt.Printf("workspace: %s\n", workspace)

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

	output, err := os.ReadFile(filepath.Join(workspace, outputFilename))
	if err != nil {
		return fmt.Errorf("read verified output: %w", err)
	}
	if string(output) != inputContent {
		return fmt.Errorf("copied file content does not match input")
	}
	fmt.Printf("[client] verified %s\n", outputFilename)
	return nil
}

func prepareWorkspace(configured string) (string, func(), error) {
	if configured != "" {
		workspace, err := filepath.Abs(configured)
		if err != nil {
			return "", nil, fmt.Errorf("resolve workspace: %w", err)
		}
		if err := os.MkdirAll(workspace, 0o755); err != nil {
			return "", nil, fmt.Errorf("create workspace: %w", err)
		}
		runWorkspace, err := os.MkdirTemp(workspace, "run-*")
		if err != nil {
			return "", nil, fmt.Errorf("create run workspace: %w", err)
		}
		return runWorkspace, func() {}, nil
	}
	workspace, err := os.MkdirTemp("", "acp-client-files-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temporary workspace: %w", err)
	}
	runWorkspace, err := os.MkdirTemp(workspace, "run-*")
	if err != nil {
		_ = os.RemoveAll(workspace)
		return "", nil, fmt.Errorf("create run workspace: %w", err)
	}
	return runWorkspace, func() {
		_ = os.RemoveAll(workspace)
	}, nil
}

func exampleDir() (string, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("resolve example source path")
	}
	return filepath.Abs(filepath.Dir(filepath.Dir(filename)))
}

func (c *exampleClient) RequestPermission(
	_ context.Context,
	request acpsdk.RequestPermissionRequest,
) (acpsdk.RequestPermissionResponse, error) {
	title := ""
	if request.ToolCall.Title != nil {
		title = *request.ToolCall.Title
	}
	fmt.Printf("\n[permission] %s\n", title)
	for _, option := range request.Options {
		fmt.Printf("[permission option] %s (%s)\n", option.Name, option.Kind)
		if option.Kind == acpsdk.PermissionOptionKindAllowOnce {
			return selectedPermission(option.OptionId), nil
		}
	}
	for _, option := range request.Options {
		if option.Kind == acpsdk.PermissionOptionKindAllowAlways {
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

func (c *exampleClient) ReadTextFile(
	ctx context.Context,
	request acpsdk.ReadTextFileRequest,
) (acpsdk.ReadTextFileResponse, error) {
	if err := ctx.Err(); err != nil {
		return acpsdk.ReadTextFileResponse{}, err
	}
	relativePath, path, err := resolveClientPath(c.workspace, request.Path)
	if err != nil {
		return acpsdk.ReadTextFileResponse{}, err
	}
	root, err := os.OpenRoot(c.workspace)
	if err != nil {
		return acpsdk.ReadTextFileResponse{}, fmt.Errorf("open workspace root: %w", err)
	}
	defer root.Close()
	file, err := root.Open(relativePath)
	if err != nil {
		return acpsdk.ReadTextFileResponse{}, fmt.Errorf("read %s: %w", path, err)
	}
	defer file.Close()
	content, err := readTextFile(file, request.Line, request.Limit)
	if err != nil {
		return acpsdk.ReadTextFileResponse{}, fmt.Errorf("read %s: %w", path, err)
	}
	fmt.Printf("[client] read %s (%d bytes)\n", path, len(content))
	return acpsdk.ReadTextFileResponse{Content: content}, nil
}

func (c *exampleClient) WriteTextFile(
	ctx context.Context,
	request acpsdk.WriteTextFileRequest,
) (acpsdk.WriteTextFileResponse, error) {
	if err := ctx.Err(); err != nil {
		return acpsdk.WriteTextFileResponse{}, err
	}
	relativePath, path, err := resolveClientPath(c.workspace, request.Path)
	if err != nil {
		return acpsdk.WriteTextFileResponse{}, err
	}
	root, err := os.OpenRoot(c.workspace)
	if err != nil {
		return acpsdk.WriteTextFileResponse{}, fmt.Errorf("open workspace root: %w", err)
	}
	defer root.Close()
	if directory := filepath.Dir(relativePath); directory != "." {
		if err := mkdirAllInRoot(root, directory, 0o755); err != nil {
			return acpsdk.WriteTextFileResponse{}, fmt.Errorf("create %s: %w", path, err)
		}
	}
	file, err := root.OpenFile(
		relativePath,
		os.O_WRONLY|os.O_CREATE|os.O_TRUNC,
		0o644,
	)
	if err != nil {
		return acpsdk.WriteTextFileResponse{}, fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := io.WriteString(file, request.Content); err != nil {
		_ = file.Close()
		return acpsdk.WriteTextFileResponse{}, fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return acpsdk.WriteTextFileResponse{}, fmt.Errorf("close %s: %w", path, err)
	}
	fmt.Printf("[client] wrote %s (%d bytes)\n", path, len(request.Content))
	return acpsdk.WriteTextFileResponse{}, nil
}

func resolveClientPath(workspace, path string) (string, string, error) {
	if strings.TrimSpace(path) == "" {
		return "", "", errors.New("path is required")
	}
	root := filepath.Clean(workspace)
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate = filepath.Clean(candidate)
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", "", fmt.Errorf("resolve path: %w", err)
	}
	if relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(relative) {
		return "", "", errors.New("path is outside the workspace")
	}
	return relative, candidate, nil
}

func readTextFile(reader io.Reader, line, limit *int) (string, error) {
	buffered := bufio.NewReaderSize(reader, readTextFileBuffer)
	startLine := 0
	if line != nil && *line > 0 {
		startLine = *line - 1
	}
	maxLines := 0
	if limit != nil && *limit > 0 {
		maxLines = *limit
	}

	var lines []string
	var current strings.Builder
	lineIndex := 0
	selectedBytes := 0
	for {
		fragment, readErr := buffered.ReadSlice('\n')
		hasNewline := len(fragment) > 0 && fragment[len(fragment)-1] == '\n'
		if hasNewline {
			fragment = fragment[:len(fragment)-1]
		}
		selecting := lineIndex >= startLine &&
			(maxLines == 0 || len(lines) < maxLines)
		if selecting {
			separatorBytes := 0
			if len(lines) > 0 {
				separatorBytes = 1
			}
			if selectedBytes+current.Len()+separatorBytes+len(fragment) >
				maxReadTextFileBytes {
				return "", fmt.Errorf(
					"requested text exceeds the %d-byte response limit",
					maxReadTextFileBytes,
				)
			}
			_, _ = current.Write(fragment)
		}
		if hasNewline {
			if selecting {
				if len(lines) > 0 {
					selectedBytes++
				}
				selectedBytes += current.Len()
				lines = append(lines, current.String())
				current.Reset()
				if maxLines > 0 && len(lines) == maxLines {
					return strings.Join(lines, "\n"), nil
				}
			}
			lineIndex++
		}

		switch {
		case readErr == nil || errors.Is(readErr, bufio.ErrBufferFull):
			continue
		case errors.Is(readErr, io.EOF):
			if selecting && !hasNewline {
				if len(lines) > 0 {
					selectedBytes++
				}
				selectedBytes += current.Len()
				lines = append(lines, current.String())
			}
			return strings.Join(lines, "\n"), nil
		default:
			return "", fmt.Errorf("read requested text: %w", readErr)
		}
	}
}

func mkdirAllInRoot(root *os.Root, directory string, permission os.FileMode) error {
	current := "."
	for _, component := range strings.Split(filepath.Clean(directory), string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		if err := root.Mkdir(current, permission); err == nil {
			continue
		} else {
			info, statErr := root.Stat(current)
			if statErr == nil && info.IsDir() {
				continue
			}
			return err
		}
	}
	return nil
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
