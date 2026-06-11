//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
)

const sessionWorkspaceIDSanitizationMarker = "SESSION_WORKSPACE_ID_SANITIZATION_OK"

func runSessionWorkspaceIDSanitization(ctx context.Context, cfg config) error {
	rt := newRuntime(cfg, sandbox.WorkspaceWriteProfile(), 1<<20, 3*time.Second)
	colonWS, err := rt.CreateWorkspace(ctx, "app/user:a/session", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	underscoreWS, err := rt.CreateWorkspace(ctx, "app/user_a/session", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	if colonWS.Path == underscoreWS.Path || colonWS.ID == underscoreWS.ID {
		return fmt.Errorf(
			"workspace IDs collided: %s/%s vs %s/%s",
			colonWS.Path,
			colonWS.ID,
			underscoreWS.Path,
			underscoreWS.ID,
		)
	}
	if err := rt.PutFiles(ctx, colonWS, []codeexecutor.PutFile{{
		Path:    "work/marker.txt",
		Content: []byte(sessionWorkspaceIDSanitizationMarker),
	}}); err != nil {
		return err
	}
	files, err := rt.Collect(ctx, underscoreWS, []string{"work/marker.txt"})
	if err != nil {
		return err
	}
	if len(files) != 0 {
		return fmt.Errorf("sanitized sibling workspace saw marker from %s: %#v", filepath.Base(colonWS.Path), files)
	}
	fmt.Println(sessionWorkspaceIDSanitizationMarker)
	return nil
}
