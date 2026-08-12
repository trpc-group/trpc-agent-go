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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
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
	cleans  []string
}

func (m *resolverStubMgr) CreateWorkspace(
	_ context.Context,
	id string,
	_ codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	m.created = append(m.created, id)
	return codeexecutor.Workspace{ID: id, Path: "/tmp/" + id}, nil
}

func (m *resolverStubMgr) Cleanup(_ context.Context, ws codeexecutor.Workspace) error {
	m.cleans = append(m.cleans, ws.ID)
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
	require.True(t, fallback.Describe().SupportsCleanEnv)
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

	// Reuse through fallback workspace name when no invocation session is present.
	ws2, err := r.CreateWorkspace(ctx, eng, "workspace")
	require.NoError(t, err)
	require.Equal(t, ws, ws2)
	require.Equal(t, []string{"workspace"}, mgr.created)

	inv := agent.NewInvocation()
	inv.Session = &session.Session{ID: "sess-123"}
	ctx = agent.NewInvocationContext(context.Background(), inv)
	ws3, err := r.CreateWorkspace(ctx, eng, "ignored-name")
	require.NoError(t, err)
	require.Equal(t, KeyFromInvocation(inv), ws3.ID)
	require.Equal(t, []string{"workspace", KeyFromInvocation(inv)}, mgr.created)

	ws4, err := r.CreateWorkspace(ctx, eng, "ignored-name")
	require.NoError(t, err)
	require.Equal(t, ws3, ws4)
	require.Equal(t, []string{"workspace", KeyFromInvocation(inv)}, mgr.created)

	inv.Session = &session.Session{
		AppName: "app",
		UserID:  "user",
		ID:      "sess-456",
	}
	ctx = agent.NewInvocationContext(context.Background(), inv)
	ws5, err := r.CreateWorkspace(ctx, eng, "ignored-name")
	require.NoError(t, err)
	require.Equal(t, KeyFromInvocation(inv), ws5.ID)
	require.Equal(t, []string{"workspace", "0:/0:/8:sess-123", KeyFromInvocation(inv)}, mgr.created)
}

// artifactProbeManager asserts CreateWorkspace's context can resolve an artifact
// the same way init hooks and StageInputs do after resolver injects service.
type artifactProbeManager struct {
	t       *testing.T
	version *int
	sawOK   bool
}

func (m *artifactProbeManager) CreateWorkspace(
	ctx context.Context,
	id string,
	_ codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	data, _, _, err := codeexecutor.LoadArtifactHelper(
		ctx,
		"app/requirements.txt",
		m.version,
	)
	require.NoError(m.t, err)
	require.Equal(m.t, "numpy==1\n", string(data))
	m.sawOK = true
	return codeexecutor.Workspace{ID: id, Path: "/tmp/" + id}, nil
}

func (*artifactProbeManager) Cleanup(context.Context, codeexecutor.Workspace) error {
	return nil
}

func TestResolver_CreateWorkspace_InjectsArtifactContext(t *testing.T) {
	svc := inmemory.NewService()
	sess := &session.Session{
		ID: "sess-art", AppName: "myapp", UserID: "u1",
	}
	info := artifact.SessionInfo{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	v, err := svc.SaveArtifact(
		context.Background(),
		info,
		"app/requirements.txt",
		&artifact.Artifact{Data: []byte("numpy==1\n")},
	)
	require.NoError(t, err)

	probe := &artifactProbeManager{t: t, version: &v}
	eng := codeexecutor.NewEngine(
		probe,
		&resolverStubFS{},
		&resolverStubRunner{},
	)

	inv := agent.NewInvocation()
	inv.Session = sess
	inv.ArtifactService = svc
	ctx := agent.NewInvocationContext(context.Background(), inv)

	r := NewResolver(nil, nil)
	ws, err := r.CreateWorkspace(ctx, eng, "ignored")
	require.NoError(t, err)
	require.Equal(t, KeyFromInvocation(inv), ws.ID)
	require.True(t, probe.sawOK)
}

type resolverInstanceManager struct {
	instanceID codeexecutor.WorkspaceInstanceID
	creates    int
}

func (m *resolverInstanceManager) CreateWorkspace(
	_ context.Context,
	id string,
	_ codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	m.creates++
	return codeexecutor.Workspace{ID: id, Path: "/tmp/" + id}, nil
}

func (*resolverInstanceManager) Cleanup(
	context.Context,
	codeexecutor.Workspace,
) error {
	return nil
}

func (m *resolverInstanceManager) InstanceID(
	context.Context,
) (codeexecutor.WorkspaceInstanceID, error) {
	return m.instanceID, nil
}

func TestResolver_InvalidateWorkspaceHandle_UsesInvocationKey(t *testing.T) {
	mgr := &resolverInstanceManager{instanceID: "instance-1"}
	eng := codeexecutor.NewEngine(
		mgr,
		&resolverStubFS{},
		&resolverStubRunner{},
	)
	r := NewResolver(nil, nil)
	inv := agent.NewInvocation()
	inv.Session = &session.Session{
		AppName: "app",
		UserID:  "user",
		ID:      "session",
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	handle, err := r.CreateWorkspaceHandle(ctx, eng, "fallback")
	require.NoError(t, err)
	require.Equal(t, 1, mgr.creates)
	require.False(t, NewResolver(nil, nil).InvalidateWorkspaceHandle(handle))
	require.True(t, r.InvalidateWorkspaceHandle(handle))

	_, err = r.CreateWorkspace(ctx, eng, "fallback")
	require.NoError(t, err)
	require.Equal(t, 2, mgr.creates)
}

func TestKeyFromInvocation_Injective(t *testing.T) {
	a := KeyFromInvocation(&agent.Invocation{Session: &session.Session{AppName: "a/b", UserID: "c", ID: "d"}})
	b := KeyFromInvocation(&agent.Invocation{Session: &session.Session{AppName: "a", UserID: "b/c", ID: "d"}})
	require.NotEqual(t, a, b)
}

func TestKeyFromInvocation_RejectsEmptyID(t *testing.T) {
	require.Equal(t, "", KeyFromInvocation(&agent.Invocation{Session: &session.Session{}}))
	require.Equal(t, "", KeyFromInvocation(&agent.Invocation{Session: &session.Session{AppName: "a", UserID: "u", ID: ""}}))
	require.NotEqual(t, "", KeyFromInvocation(&agent.Invocation{Session: &session.Session{ID: "x"}}))
}

func TestLegacyKeyFromInvocation_MatchesOldFormat(t *testing.T) {
	// Full identity: old format was "app/user/id".
	require.Equal(t, "app/user/sid",
		LegacyKeyFromInvocation(&agent.Invocation{Session: &session.Session{
			AppName: "app", UserID: "user", ID: "sid",
		}}))

	// Missing app or user: old format fell back to just "id".
	require.Equal(t, "sid",
		LegacyKeyFromInvocation(&agent.Invocation{Session: &session.Session{
			ID: "sid",
		}}))
	require.Equal(t, "sid",
		LegacyKeyFromInvocation(&agent.Invocation{Session: &session.Session{
			AppName: "app", ID: "sid",
		}}))

	// Empty ID: returns "" (same as new format).
	require.Equal(t, "",
		LegacyKeyFromInvocation(&agent.Invocation{Session: &session.Session{}}))

	// Nil safety.
	require.Equal(t, "", LegacyKeyFromInvocation(nil))
}

func TestKeyFromInvocation_DiffersFromLegacy(t *testing.T) {
	inv := &agent.Invocation{Session: &session.Session{
		AppName: "app", UserID: "user", ID: "sid",
	}}
	require.NotEqual(t, KeyFromInvocation(inv), LegacyKeyFromInvocation(inv),
		"new and legacy keys must differ to justify the migration")
}

func TestResolver_CreateWorkspace_EmptySessionIDUsesEphemeralKey(t *testing.T) {
	mgr := &resolverStubMgr{}
	eng := newResolverStubEngine(mgr)
	r := NewResolver(nil, nil)
	inv := agent.NewInvocation()
	inv.Session = &session.Session{AppName: "app", UserID: "u", ID: ""}
	ctx := agent.NewInvocationContext(context.Background(), inv)
	ws1, err := r.CreateWorkspace(ctx, eng, "skill-name")
	require.NoError(t, err)
	require.NotEqual(t, "skill-name", ws1.ID)
	require.True(t, strings.HasPrefix(ws1.ID, "ephemeral-invocation-"),
		"ephemeral key must be derived from InvocationID")
	require.Contains(t, ws1.ID, inv.InvocationID,
		"ephemeral key must embed the InvocationID for per-invocation stability")

	// Same invocation reuses the same workspace (cached by stable key).
	ws1b, err := r.CreateWorkspace(ctx, eng, "skill-name")
	require.NoError(t, err)
	require.Equal(t, ws1, ws1b, "same invocation must reuse the ephemeral workspace")
	require.Len(t, mgr.created, 1, "second call must hit cache, not create a new workspace")

	// Different invocation gets a different workspace.
	inv2 := agent.NewInvocation()
	inv2.Session = &session.Session{}
	ctx2 := agent.NewInvocationContext(context.Background(), inv2)
	ws2, err := r.CreateWorkspace(ctx2, eng, "skill-name")
	require.NoError(t, err)
	require.NotEqual(t, ws1.ID, ws2.ID, "different invocations must get different ephemeral keys")
	require.NotContains(t, mgr.created, "skill-name")
}

// TestResolver_ReleaseWorkspaceHandle_CleansEphemeralWorkspace verifies
// that the public ReleaseWorkspaceHandle method actually invokes Cleanup
// on the manager — not just returning nil. This guards against regressions
// where the release path silently becomes a no-op.
func TestResolver_ReleaseWorkspaceHandle_CleansEphemeralWorkspace(t *testing.T) {
	mgr := &resolverStubMgr{}
	eng := newResolverStubEngine(mgr)
	r := NewResolver(nil, nil)
	inv := agent.NewInvocation()
	inv.Session = &session.Session{AppName: "app", UserID: "u", ID: ""}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	handle, err := r.CreateWorkspaceHandle(ctx, eng, "skill-name")
	require.NoError(t, err)
	require.NotEqual(t, "", handle.Workspace.ID, "handle must reference a workspace")
	require.Len(t, mgr.cleans, 0, "no cleanup before Release")

	// ReleaseWorkspaceHandle must actually call Cleanup on the manager.
	require.NoError(t, r.ReleaseWorkspaceHandle(ctx, handle))
	require.Len(t, mgr.cleans, 1, "Release must invoke Cleanup exactly once")
	require.Equal(t, handle.Workspace.ID, mgr.cleans[0],
		"Cleanup must be called for the released workspace")

	// After release, the workspace must no longer be in the registry cache.
	_, ok := r.reg.Get(handle.Workspace.ID)
	require.False(t, ok, "released workspace must not remain in registry cache")
}
