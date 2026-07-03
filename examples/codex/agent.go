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
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/codex"
)

// codexSettings holds command-line settings for the Codex CLI agent.
type codexSettings struct {
	bin            string
	model          string
	mcpURL         string
	approvalPolicy string
	sandbox        string
	workDir        string
	logDir         string
}

// newCodexAgent builds a Codex CLI agent with the provided settings.
func newCodexAgent(settings codexSettings) (agent.Agent, error) {
	opts := []codex.Option{
		codex.WithBin(strings.TrimSpace(settings.bin)),
	}
	opts = appendRemoteMCPServer(opts, settings.mcpURL)
	if strings.TrimSpace(settings.approvalPolicy) != "" {
		opts = append(opts, codex.WithGlobalArgs("--ask-for-approval", strings.TrimSpace(settings.approvalPolicy)))
	}
	if strings.TrimSpace(settings.model) != "" {
		opts = append(opts, codex.WithExtraArgs("--model", strings.TrimSpace(settings.model)))
	}
	if strings.TrimSpace(settings.sandbox) != "" {
		opts = append(opts, codex.WithGlobalArgs("--sandbox", strings.TrimSpace(settings.sandbox)))
	}
	if strings.TrimSpace(settings.workDir) != "" {
		opts = append(opts, codex.WithWorkDir(strings.TrimSpace(settings.workDir)))
	}
	if strings.TrimSpace(settings.logDir) != "" {
		opts = append(opts, codex.WithRawOutputHook(newLogHook(strings.TrimSpace(settings.logDir))))
	}
	return codex.New(opts...)
}

// appendRemoteMCPServer injects a temporary streamable HTTP MCP server without changing Codex config files.
func appendRemoteMCPServer(opts []codex.Option, mcpURL string) []codex.Option {
	trimmed := strings.TrimSpace(mcpURL)
	if trimmed == "" {
		return opts
	}
	return append(opts, codex.WithGlobalArgs(
		"-c", fmt.Sprintf("mcp_servers.codex_cli_example.url=%q", trimmed),
		"-c", `mcp_servers.codex_cli_example.default_tools_approval_mode="approve"`,
	))
}

// newLogHook returns a hook that appends raw CLI output into a thread-scoped log file.
func newLogHook(outDir string) codex.RawOutputHook {
	logDir := filepath.Join(outDir, "codex-cli-logs")
	return func(_ context.Context, args *codex.RawOutputHookArgs) error {
		if args == nil {
			return nil
		}
		logKey := strings.TrimSpace(args.ThreadID)
		if logKey == "" {
			logKey = strings.TrimSpace(args.ResumeThreadID)
		}
		if logKey == "" {
			logKey = strings.TrimSpace(args.SessionID)
		}
		if logKey == "" {
			return nil
		}
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return fmt.Errorf("create log dir: %w", err)
		}
		logPath := filepath.Join(logDir, fmt.Sprintf("%s.log.txt", logKey))
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		defer func() { _ = f.Close() }()
		if _, err := fmt.Fprintf(f, "\n\n===== invocation %s =====\n", args.InvocationID); err != nil {
			return err
		}
		if err := writeLogField(f, "session_id", args.SessionID); err != nil {
			return err
		}
		if err := writeLogField(f, "resume_thread_id", args.ResumeThreadID); err != nil {
			return err
		}
		if err := writeLogField(f, "thread_id", args.ThreadID); err != nil {
			return err
		}
		if err := writeLogField(f, "prompt", args.Prompt); err != nil {
			return err
		}
		if err := writeLogBytes(f, "stdout", args.Stdout); err != nil {
			return err
		}
		if err := writeLogBytes(f, "stderr", args.Stderr); err != nil {
			return err
		}
		if args.Error != nil {
			if _, err := fmt.Fprintln(f, "error: "+args.Error.Error()); err != nil {
				return err
			}
		}
		return nil
	}
}

// writeLogField writes a non-empty key-value field to the log file.
func writeLogField(f *os.File, key string, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	_, err := fmt.Fprintln(f, key+": "+value)
	return err
}

// writeLogBytes writes a labeled byte block to the log file.
func writeLogBytes(f *os.File, label string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(f, label+":"); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		if _, err := f.Write([]byte("\n")); err != nil {
			return err
		}
	}
	return nil
}
