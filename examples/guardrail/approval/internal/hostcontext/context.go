//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package hostcontext exposes the effective host execution target to reviewers.
package hostcontext

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Callbacks resolves exec_command workdir before the final permission check.
// baseDir must be absolute, matching the hostexec tool set configuration.
func Callbacks(baseDir string) *tool.Callbacks {
	callbacks := tool.NewCallbacks()
	callbacks.RegisterBeforeTool(func(_ context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		if args.ToolName != "hostexec_exec_command" {
			return nil, nil
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(args.Arguments, &fields); err != nil || fields == nil {
			return nil, fmt.Errorf("exec arguments must be a JSON object")
		}
		var workdir string
		if raw, ok := fields["workdir"]; ok {
			if err := json.Unmarshal(raw, &workdir); err != nil {
				return nil, fmt.Errorf("decode workdir: %w", err)
			}
		}
		workdir = strings.TrimSpace(workdir)
		if workdir == "~" || strings.HasPrefix(workdir, "~/") {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			if workdir == "~" {
				workdir = homeDir
			} else {
				workdir = filepath.Join(homeDir, strings.TrimPrefix(workdir, "~/"))
			}
		} else if workdir == "" {
			workdir = baseDir
		} else if !filepath.IsAbs(workdir) {
			workdir = filepath.Join(baseDir, workdir)
		}
		fields["workdir"], _ = json.Marshal(workdir)
		updated, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		return &tool.BeforeToolResult{ModifiedArguments: updated}, nil
	})
	return callbacks
}

// Provider supplies host facts without copying ambient environment variables.
func Provider(baseDir string) func(context.Context, *review.Request) (review.Supplement, error) {
	return func(_ context.Context, _ *review.Request) (review.Supplement, error) {
		return review.Supplement{
			ReviewInstructions: "Require authorization for the exact command, target directory, and material side effects. Do not assume a workspace default is a filesystem restriction.",
			Facts: []review.Fact{
				{Source: "hostexec configuration", Content: "Commands execute on the local host with the application's operating-system permissions. There is no sandbox. The default directory is " + baseDir + ". An exec_command action includes its resolved absolute workdir."},
			},
		}, nil
	}
}
