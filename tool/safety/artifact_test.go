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
)

func TestRedactArtifact_TextRedactsSecret(t *testing.T) {
	in := &artifact.Artifact{
		MimeType: "text/plain",
		Data:     []byte("API_KEY=sk_live_1234567890abcdef1234"),
	}
	out, changed, err := redactArtifact(in)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotContains(t, string(out.Data), "sk_live_1234567890abcdef1234")
	require.Contains(t, string(out.Data), "[REDACTED:")
}

func TestRedactArtifact_BinaryWithSecretRejected(t *testing.T) {
	in := &artifact.Artifact{
		MimeType: "application/octet-stream",
		Data:     []byte("API_KEY=sk_live_1234567890abcdef1234"),
	}
	_, _, err := redactArtifact(in)
	require.Error(t, err)
}

func TestRedactArtifact_NoSecretUnchanged(t *testing.T) {
	in := &artifact.Artifact{
		MimeType: "text/plain",
		Data:     []byte("hello world"),
	}
	out, changed, err := redactArtifact(in)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, in, out)
}

func TestRedactArtifact_JSONUsesFieldAwareRedaction(t *testing.T) {
	in := &artifact.Artifact{
		MimeType: "application/json; charset=utf-8",
		Data: []byte(
			`{"password":"hunter2xyz","value":9007199254740993}`,
		),
	}
	out, changed, err := redactArtifact(in)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotContains(t, string(out.Data), "hunter2xyz")
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(out.Data, &decoded))
	require.Contains(t, string(out.Data), "9007199254740993")
}

func TestRedactArtifact_InvalidJSONFailsClosed(t *testing.T) {
	in := &artifact.Artifact{
		MimeType: "application/json",
		Data:     []byte(`{"password":`),
	}
	_, _, err := redactArtifact(in)
	require.Error(t, err)
}

func TestRedactArtifact_NilSafe(t *testing.T) {
	out, changed, err := redactArtifact(nil)
	require.NoError(t, err)
	require.False(t, changed)
	require.Nil(t, out)
}

type stubArtifactService struct {
	saved   *artifact.Artifact
	loaded  *artifact.Artifact
	keys    []string
	saveErr error
}

func (s *stubArtifactService) SaveArtifact(_ context.Context, _ artifact.SessionInfo, _ string, a *artifact.Artifact) (int, error) {
	if s.saveErr != nil {
		return 0, s.saveErr
	}
	s.saved = a
	return 0, nil
}

func (s *stubArtifactService) LoadArtifact(_ context.Context, _ artifact.SessionInfo, _ string, _ *int) (*artifact.Artifact, error) {
	return s.loaded, nil
}

func (s *stubArtifactService) ListArtifactKeys(_ context.Context, _ artifact.SessionInfo) ([]string, error) {
	return s.keys, nil
}

func (s *stubArtifactService) DeleteArtifact(_ context.Context, _ artifact.SessionInfo, _ string) error {
	return nil
}

func (s *stubArtifactService) ListVersions(_ context.Context, _ artifact.SessionInfo, _ string) ([]int, error) {
	return nil, nil
}

func TestArtifactServiceWrapper_RedactsOnSave(t *testing.T) {
	stub := &stubArtifactService{}
	wrapped := newArtifactServiceWrapper(stub)
	in := &artifact.Artifact{
		MimeType: "text/plain",
		Data:     []byte("Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"),
	}
	_, err := wrapped.SaveArtifact(
		context.Background(), artifact.SessionInfo{}, "file.txt", in,
	)
	require.NoError(t, err)
	require.NotNil(t, stub.saved)
	require.NotEqual(t, string(in.Data), string(stub.saved.Data))
}

func TestArtifactServiceWrapper_CopiesCleanArtifactData(t *testing.T) {
	stub := &stubArtifactService{}
	wrapped := newArtifactServiceWrapper(stub)
	in := &artifact.Artifact{
		MimeType: "text/plain",
		Data:     []byte("clean"),
	}
	_, err := wrapped.SaveArtifact(
		context.Background(), artifact.SessionInfo{}, "file.txt", in,
	)
	require.NoError(t, err)
	in.Data[0] = 'X'
	require.Equal(t, "clean", string(stub.saved.Data))

	stub.loaded = &artifact.Artifact{
		MimeType: "text/plain",
		Data:     []byte("loaded"),
	}
	out, err := wrapped.LoadArtifact(
		context.Background(), artifact.SessionInfo{}, "file.txt", nil,
	)
	require.NoError(t, err)
	out.Data[0] = 'X'
	require.Equal(t, "loaded", string(stub.loaded.Data))
}

func TestArtifactServiceWrapper_RefusesBinarySecretOnSave(t *testing.T) {
	stub := &stubArtifactService{}
	wrapped := newArtifactServiceWrapper(stub)
	in := &artifact.Artifact{
		MimeType: "application/octet-stream",
		Data:     []byte("API_KEY=sk_live_1234567890abcdef1234"),
	}
	_, err := wrapped.SaveArtifact(context.Background(), artifact.SessionInfo{}, "file.bin", in)
	require.Error(t, err)
}

// TestArtifactServiceWrapper_RefusesSecretFilename verifies that a
// secret-bearing filename is rejected before it becomes a persisted
// storage key that ListArtifactKeys would expose.
func TestArtifactServiceWrapper_RefusesSecretFilename(t *testing.T) {
	stub := &stubArtifactService{}
	wrapped := newArtifactServiceWrapper(stub)
	in := &artifact.Artifact{
		MimeType: "text/plain",
		Data:     []byte("harmless"),
	}
	_, err := wrapped.SaveArtifact(context.Background(), artifact.SessionInfo{},
		"creds-AKIAIOSFODNN7EXAMPLE.txt", in)
	require.Error(t, err)
	// The inner service must never see the save.
	require.Nil(t, stub.saved)
}

func TestArtifactServiceWrapper_RedactsOnLoad(t *testing.T) {
	stub := &stubArtifactService{
		loaded: &artifact.Artifact{
			MimeType: "text/plain",
			Data:     []byte("AKIAIOSFODNN7EXAMPLE"),
		},
	}
	wrapped := newArtifactServiceWrapper(stub)
	out, err := wrapped.LoadArtifact(context.Background(), artifact.SessionInfo{}, "file.txt", nil)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.False(t, strings.Contains(string(out.Data), "AKIAIOSFODNN7EXAMPLE"))
}

func TestArtifactServiceWrapper_RefusesSecretExistingKey(t *testing.T) {
	stub := &stubArtifactService{
		keys: []string{"safe.txt", "AKIAIOSFODNN7EXAMPLE.txt"},
	}
	wrapped := newArtifactServiceWrapper(stub)
	_, err := wrapped.ListArtifactKeys(
		context.Background(), artifact.SessionInfo{},
	)
	require.ErrorContains(t, err, "artifact key contains a secret")
}
