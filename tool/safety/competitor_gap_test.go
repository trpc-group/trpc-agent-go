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
	saved    *artifact.Artifact
	name     string
	keys     []string
	versions []int
	deleted  string
}

func (m *memArtifact) SaveArtifact(_ context.Context, _ artifact.SessionInfo, filename string, value *artifact.Artifact) (int, error) {
	m.name = filename
	if value != nil {
		cp := *value
		cp.Data = append([]byte(nil), value.Data...)
		m.saved = &cp
	} else {
		m.saved = nil
	}
	return 0, nil
}
func (m *memArtifact) LoadArtifact(context.Context, artifact.SessionInfo, string, *int) (*artifact.Artifact, error) {
	return m.saved, nil
}
func (m *memArtifact) ListArtifactKeys(context.Context, artifact.SessionInfo) ([]string, error) {
	return append([]string(nil), m.keys...), nil
}
func (m *memArtifact) DeleteArtifact(_ context.Context, _ artifact.SessionInfo, filename string) error {
	m.deleted = filename
	return nil
}
func (m *memArtifact) ListVersions(context.Context, artifact.SessionInfo, string) ([]int, error) {
	return append([]int(nil), m.versions...), nil
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

func TestRedactingArtifactService_DelegatesAndEdges(t *testing.T) {
	t.Parallel()
	inner := &memArtifact{
		keys:     []string{"a.txt", "b.json"},
		versions: []int{0, 1},
	}
	svc := NewRedactingArtifactService(inner)
	ctx := context.Background()
	si := artifact.SessionInfo{}

	// Unchanged text path (no secret).
	_, err := svc.SaveArtifact(ctx, si, "notes.txt", &artifact.Artifact{
		Data:     []byte("hello world"),
		MimeType: "text/plain",
	})
	require.NoError(t, err)
	require.Equal(t, []byte("hello world"), inner.saved.Data)

	// Empty / nil value.
	_, err = svc.SaveArtifact(ctx, si, "empty.txt", &artifact.Artifact{Data: nil, MimeType: "text/plain"})
	require.NoError(t, err)
	_, err = svc.SaveArtifact(ctx, si, "nil", nil)
	require.NoError(t, err)

	// JSON scrub via mime +json and .json extension.
	token := "sk-" + strings.Repeat("e", 32)
	_, err = svc.SaveArtifact(ctx, si, "cfg.json", &artifact.Artifact{
		Data:     []byte(`{"token":"` + token + `"}`),
		MimeType: "application/json",
	})
	require.NoError(t, err)
	require.NotContains(t, string(inner.saved.Data), token)

	_, err = svc.SaveArtifact(ctx, si, "payload", &artifact.Artifact{
		Data:     []byte(`{"k":"` + token + `"}`),
		MimeType: "application/vnd.api+json",
	})
	require.NoError(t, err)
	require.NotContains(t, string(inner.saved.Data), token)

	// Safe binary passes through.
	_, err = svc.SaveArtifact(ctx, si, "ok.bin", &artifact.Artifact{
		Data:     []byte{0x00, 0x01, 0x02},
		MimeType: "application/octet-stream",
	})
	require.NoError(t, err)
	require.Equal(t, []byte{0x00, 0x01, 0x02}, inner.saved.Data)

	// Text via extension without mime.
	_, err = svc.SaveArtifact(ctx, si, "notes.md", &artifact.Artifact{
		Data: []byte("token=" + token),
	})
	require.NoError(t, err)
	require.Contains(t, string(inner.saved.Data), "REDACTED")

	got, err := svc.LoadArtifact(ctx, si, "notes.md", nil)
	require.NoError(t, err)
	require.Equal(t, inner.saved, got)

	keys, err := svc.ListArtifactKeys(ctx, si)
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt", "b.json"}, keys)

	require.NoError(t, svc.DeleteArtifact(ctx, si, "a.txt"))
	require.Equal(t, "a.txt", inner.deleted)

	vers, err := svc.ListVersions(ctx, si, "a.txt")
	require.NoError(t, err)
	require.Equal(t, []int{0, 1}, vers)

	// Nil inner / nil receiver.
	nilSvc := NewRedactingArtifactService(nil)
	_, err = nilSvc.SaveArtifact(ctx, si, "x", &artifact.Artifact{Data: []byte("a")})
	require.Error(t, err)
	_, err = nilSvc.LoadArtifact(ctx, si, "x", nil)
	require.Error(t, err)
	_, err = nilSvc.ListArtifactKeys(ctx, si)
	require.Error(t, err)
	require.Error(t, nilSvc.DeleteArtifact(ctx, si, "x"))
	_, err = nilSvc.ListVersions(ctx, si, "x")
	require.Error(t, err)
	_, err = (*RedactingArtifactService)(nil).LoadArtifact(ctx, si, "x", nil)
	require.Error(t, err)
}

func TestResource_DDUnboundedDevice(t *testing.T) {
	t.Parallel()
	// dd is typically deny-listed; assert the unbounded-device helper directly.
	for _, cmd := range []string{
		"dd if=/dev/zero of=/tmp/x",
		"dd if=/dev/random of=out.bin",
	} {
		f, ok := scanUnboundedDeviceIO(cmd, cmd)
		require.True(t, ok, cmd)
		require.Equal(t, "resource.unbounded_device", f.RuleID)
	}
	g := NewGuard(WithPolicy(DefaultPolicy()))
	raw, err := json.Marshal(map[string]any{"command": "cat /dev/random"})
	require.NoError(t, err)
	dec, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: raw,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, dec.Action)
	require.Contains(t, dec.Reason, "resource.unbounded_device")

	f, ok := scanUnboundedDeviceIO("dd if=/dev/zero of=x count=10", "dd if=/dev/zero of=x count=10")
	require.False(t, ok)
	require.Equal(t, Finding{}, f)
	_, ok = scanUnboundedDeviceIO("", "")
	require.False(t, ok)
}
