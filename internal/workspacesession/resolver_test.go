//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspacesession

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type resolverStubExec struct {
	eng codeexecutor.Engine
}

func (*resolverStubExec) ExecuteCode(
	context.Context,
	codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (*resolverStubExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func (s *resolverStubExec) Engine() codeexecutor.Engine { return s.eng }

type resolverStubMgr struct {
	created []string
}

func (m *resolverStubMgr) CreateWorkspace(
	_ context.Context,
	id string,
	_ codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	m.created = append(m.created, id)
	return codeexecutor.Workspace{ID: id, Path: "/tmp/" + id}, nil
}

func (*resolverStubMgr) Cleanup(context.Context, codeexecutor.Workspace) error {
	return nil
}

type resolverStubFS struct{}

func (*resolverStubFS) PutFiles(
	context.Context,
	codeexecutor.Workspace,
	[]codeexecutor.PutFile,
) error {
	return nil
}

func (*resolverStubFS) StageDirectory(
	context.Context,
	codeexecutor.Workspace,
	string,
	string,
	codeexecutor.StageOptions,
) error {
	return nil
}

func (*resolverStubFS) Collect(
	context.Context,
	codeexecutor.Workspace,
	[]string,
) ([]codeexecutor.File, error) {
	return nil, nil
}

func (*resolverStubFS) StageInputs(
	context.Context,
	codeexecutor.Workspace,
	[]codeexecutor.InputSpec,
) error {
	return nil
}

func (*resolverStubFS) CollectOutputs(
	context.Context,
	codeexecutor.Workspace,
	codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}

type resolverStubRunner struct{}

func (*resolverStubRunner) RunProgram(
	context.Context,
	codeexecutor.Workspace,
	codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	return codeexecutor.RunResult{}, nil
}

func newResolverStubEngine(mgr *resolverStubMgr) codeexecutor.Engine {
	return codeexecutor.NewEngine(mgr, &resolverStubFS{}, &resolverStubRunner{})
}

func TestResolver_EnsureEngine(t *testing.T) {
	mgr := &resolverStubMgr{}
	want := newResolverStubEngine(mgr)

	r := NewResolver(&resolverStubExec{eng: want}, nil)
	got := r.EnsureEngine()
	require.Same(t, want, got)

	fallback := NewResolver(nil, nil).EnsureEngine()
	require.NotNil(t, fallback)
	require.NotNil(t, fallback.Manager())
	require.NotNil(t, fallback.FS())
	require.NotNil(t, fallback.Runner())
}

func TestResolver_CreateWorkspace_UsesSessionIDOrFallbackName(t *testing.T) {
	mgr := &resolverStubMgr{}
	eng := newResolverStubEngine(mgr)
	r := NewResolver(nil, nil)

	ctx := context.Background()
	ws, err := r.CreateWorkspace(ctx, eng, "workspace")
	require.NoError(t, err)
	require.Equal(t, "workspace", ws.ID)
	require.Equal(t, []string{"workspace"}, mgr.created)

	// Reuse through registry.
	ws2, err := r.CreateWorkspace(ctx, eng, "workspace")
	require.NoError(t, err)
	require.Equal(t, ws, ws2)
	require.Equal(t, []string{"workspace"}, mgr.created)

	inv := agent.NewInvocation()
	inv.Session = &session.Session{ID: "sess-123"}
	ctx = agent.NewInvocationContext(context.Background(), inv)
	ws3, err := r.CreateWorkspace(ctx, eng, "ignored-name")
	require.NoError(t, err)
	require.Equal(t, "sess-123", ws3.ID)
	require.Equal(t, []string{"workspace", "sess-123"}, mgr.created)
}
