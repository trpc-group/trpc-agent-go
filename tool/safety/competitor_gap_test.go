//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestResource_DevZeroAndYesAsk(t *testing.T) {
	t.Parallel()
	g := NewGuard(WithPolicy(DefaultPolicy()))

	cases := []struct {
		name string
		cmd  string
		rule string
	}{
		{name: "cat_zero", cmd: "cat /dev/zero", rule: "resource.unbounded_device"},
		{name: "base64_zero", cmd: "base64 /dev/zero", rule: "resource.unbounded_device"},
		{name: "yes", cmd: "yes", rule: "resource.unbounded_yes"},
		{name: "pipe_yes", cmd: "echo hi | yes", rule: "resource.unbounded_yes"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(map[string]any{"command": tc.cmd})
			require.NoError(t, err)
			dec, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				ToolName:  "workspace_exec",
				Arguments: raw,
			})
			require.NoError(t, err)
			require.Equal(t, tool.PermissionActionAsk, dec.Action)
			require.Contains(t, dec.Reason, tc.rule)
		})
	}
}

func TestPolicyRevision_StableAndSensitive(t *testing.T) {
	t.Parallel()
	a := DefaultPolicy()
	b := DefaultPolicy()
	b.PolicyID = "prod"
	b.SchemaVersion = "9"
	require.Equal(t, a.Revision(), b.Revision(), "identity metadata must not change revision")

	c := DefaultPolicy()
	c.DeniedCommands = append(append([]string{}, c.DeniedCommands...), "nc")
	require.NotEqual(t, a.Revision(), c.Revision())

	schema, id, rev := a.Meta()
	require.Equal(t, DefaultSchemaVersion, schema)
	require.Equal(t, DefaultPolicyID, id)
	require.NotEmpty(t, rev)
}

func TestAuditEvent_IncludesPolicyMeta(t *testing.T) {
	t.Parallel()
	mem := NewMemoryAuditor()
	pol := DefaultPolicy()
	pol.PolicyID = "unit"
	pol.SchemaVersion = "1"
	g := NewGuard(WithPolicy(pol), WithAuditor(mem))
	raw, err := json.Marshal(map[string]any{"command": "rm -rf /"})
	require.NoError(t, err)
	_, err = g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: raw,
	})
	require.NoError(t, err)
	evs := mem.Events()
	require.NotEmpty(t, evs)
	require.Equal(t, "1", evs[0].SchemaVersion)
	require.Equal(t, "unit", evs[0].PolicyID)
	require.Equal(t, pol.Revision(), evs[0].PolicyRevision)
}

type memArtifact struct {
	saved *artifact.Artifact
	name  string
}

func (m *memArtifact) SaveArtifact(_ context.Context, _ artifact.SessionInfo, filename string, value *artifact.Artifact) (int, error) {
	m.name = filename
	if value != nil {
		cp := *value
		cp.Data = append([]byte(nil), value.Data...)
		m.saved = &cp
	}
	return 0, nil
}
func (m *memArtifact) LoadArtifact(context.Context, artifact.SessionInfo, string, *int) (*artifact.Artifact, error) {
	return m.saved, nil
}
func (m *memArtifact) ListArtifactKeys(context.Context, artifact.SessionInfo) ([]string, error) {
	return nil, nil
}
func (m *memArtifact) DeleteArtifact(context.Context, artifact.SessionInfo, string) error {
	return nil
}
func (m *memArtifact) ListVersions(context.Context, artifact.SessionInfo, string) ([]int, error) {
	return nil, nil
}

func TestRedactingArtifactService_ScrubsText(t *testing.T) {
	t.Parallel()
	inner := &memArtifact{}
	svc := NewRedactingArtifactService(inner)
	token := "sk-" + strings.Repeat("a", 32)
	_, err := svc.SaveArtifact(context.Background(), artifact.SessionInfo{}, "notes.txt", &artifact.Artifact{
		Data:     []byte("token=" + token),
		MimeType: "text/plain",
	})
	require.NoError(t, err)
	require.NotContains(t, string(inner.saved.Data), token)
	require.Contains(t, string(inner.saved.Data), "REDACTED")
}

func TestRedactingArtifactService_RejectsSecretBinary(t *testing.T) {
	t.Parallel()
	inner := &memArtifact{}
	svc := NewRedactingArtifactService(inner)
	token := "sk-" + strings.Repeat("b", 32)
	_, err := svc.SaveArtifact(context.Background(), artifact.SessionInfo{}, "blob.bin", &artifact.Artifact{
		Data:     []byte(token),
		MimeType: "application/octet-stream",
	})
	require.Error(t, err)
	require.Nil(t, inner.saved)
}
