//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
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
	"trpc.group/trpc-go/trpc-agent-go/agent/claudecode"
)

func newClaudeCodeEvalAgent(bin, outputFormat, workDir, logDir string) (agent.Agent, error) {
	opts := []claudecode.Option{
		claudecode.WithBin(strings.TrimSpace(bin)),
		claudecode.WithOutputFormat(claudecode.OutputFormat(strings.TrimSpace(outputFormat))),
		claudecode.WithWorkDir(strings.TrimSpace(workDir)),
		claudecode.WithExtraArgs(
			"--permission-mode", "bypassPermissions",
			"--strict-mcp-config",
			"--mcp-config", ".mcp.json",
		),
	}
	if strings.TrimSpace(logDir) != "" {
		opts = append(opts, claudecode.WithRawOutputHook(newLogHook(logDir)))
	}
	return claudecode.New(opts...)
}

func newLogHook(outDir string) claudecode.RawOutputHook {
	logDir := filepath.Join(outDir, "claude-cli-logs")
	return func(_ context.Context, args *claudecode.RawOutputHookArgs) error {
		if args == nil || strings.TrimSpace(args.CLISessionID) == "" {
			return nil
		}
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return fmt.Errorf("create log dir: %w", err)
		}
		logPath := filepath.Join(logDir, fmt.Sprintf("%s.log.txt", args.CLISessionID))
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		defer func() { _ = f.Close() }()

		if _, err := fmt.Fprintf(f, "\n\n===== invocation %s =====\n", args.InvocationID); err != nil {
			return err
		}
		if strings.TrimSpace(args.SessionID) != "" {
			if _, err := fmt.Fprintln(f, "session_id: "+args.SessionID); err != nil {
				return err
			}
		}
		if strings.TrimSpace(args.CLISessionID) != "" {
			if _, err := fmt.Fprintln(f, "cli_session_id: "+args.CLISessionID); err != nil {
				return err
			}
		}
		if strings.TrimSpace(args.Prompt) != "" {
			if _, err := fmt.Fprintln(f, "prompt: "+args.Prompt); err != nil {
				return err
			}
		}
		if len(args.Stdout) > 0 {
			if _, err := fmt.Fprintln(f, "stdout:"); err != nil {
				return err
			}
			if _, err := f.Write(args.Stdout); err != nil {
				return err
			}
			if !bytes.HasSuffix(args.Stdout, []byte("\n")) {
				if _, err := f.Write([]byte("\n")); err != nil {
					return err
				}
			}
		}
		if len(args.Stderr) > 0 {
			if _, err := fmt.Fprintln(f, "stderr:"); err != nil {
				return err
			}
			if _, err := f.Write(args.Stderr); err != nil {
				return err
			}
			if !bytes.HasSuffix(args.Stderr, []byte("\n")) {
				if _, err := f.Write([]byte("\n")); err != nil {
					return err
				}
			}
		}
		if args.Error != nil {
			if _, err := fmt.Fprintln(f, "error: "+args.Error.Error()); err != nil {
				return err
			}
		}
		return nil
	}
}
