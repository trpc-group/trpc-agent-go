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
	"fmt"
	"mime"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

// RedactingArtifactService wraps an artifact.Service and scrubs secret-looking
// text (or rejects secret-bearing binary blobs) at SaveArtifact time.
//
// PermissionPolicy never sees tool outputs or artifacts; hosts that persist
// agent files should wire this instead of inventing a second redaction path.
// It does not replace sandboxing or executor output caps.
type RedactingArtifactService struct {
	inner artifact.Service
}

// NewRedactingArtifactService returns a save-time redacting decorator.
// A nil inner is rejected at Save/Load time with a clear error.
func NewRedactingArtifactService(inner artifact.Service) *RedactingArtifactService {
	return &RedactingArtifactService{inner: inner}
}

// SaveArtifact redacts text/JSON artifacts; binary payloads that already look
// like secrets are rejected (rewriting arbitrary bytes is unsafe).
func (s *RedactingArtifactService) SaveArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
	value *artifact.Artifact,
) (int, error) {
	inner, err := s.requireInner()
	if err != nil {
		return 0, err
	}
	if value == nil {
		return inner.SaveArtifact(ctx, sessionInfo, filename, nil)
	}
	data, changed, err := redactArtifactData(filename, value.MimeType, value.Data)
	if err != nil {
		return 0, err
	}
	if !changed {
		return inner.SaveArtifact(ctx, sessionInfo, filename, value)
	}
	cp := *value
	cp.Data = data
	return inner.SaveArtifact(ctx, sessionInfo, filename, &cp)
}

// LoadArtifact delegates without modifying stored bytes.
func (s *RedactingArtifactService) LoadArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
	version *int,
) (*artifact.Artifact, error) {
	inner, err := s.requireInner()
	if err != nil {
		return nil, err
	}
	return inner.LoadArtifact(ctx, sessionInfo, filename, version)
}

// ListArtifactKeys delegates.
func (s *RedactingArtifactService) ListArtifactKeys(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
) ([]string, error) {
	inner, err := s.requireInner()
	if err != nil {
		return nil, err
	}
	return inner.ListArtifactKeys(ctx, sessionInfo)
}

// DeleteArtifact delegates.
func (s *RedactingArtifactService) DeleteArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
) error {
	inner, err := s.requireInner()
	if err != nil {
		return err
	}
	return inner.DeleteArtifact(ctx, sessionInfo, filename)
}

// ListVersions delegates.
func (s *RedactingArtifactService) ListVersions(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
) ([]int, error) {
	inner, err := s.requireInner()
	if err != nil {
		return nil, err
	}
	return inner.ListVersions(ctx, sessionInfo, filename)
}

func (s *RedactingArtifactService) requireInner() (artifact.Service, error) {
	if s == nil || s.inner == nil {
		return nil, fmt.Errorf("safety: nil artifact service")
	}
	return s.inner, nil
}

func redactArtifactData(filename, mimeType string, data []byte) ([]byte, bool, error) {
	if len(data) == 0 {
		return data, false, nil
	}
	textish := isTextArtifact(filename, mimeType)
	if !textish {
		if containsSecretEvidence(string(data)) {
			return nil, false, fmt.Errorf(
				"safety: refuse to store binary artifact %q containing a recognized secret",
				filename,
			)
		}
		return data, false, nil
	}
	if isJSONArtifact(filename, mimeType) {
		out := RedactJSON(data)
		return out, string(out) != string(data), nil
	}
	out := []byte(RedactText(string(data)))
	return out, string(out) != string(data), nil
}

func isJSONArtifact(filename, mimeType string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(mimeType))
	if err == nil {
		mediaType = strings.ToLower(mediaType)
		if mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") {
			return true
		}
	}
	return strings.EqualFold(filepath.Ext(filename), ".json")
}

func isTextArtifact(filename, mimeType string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(mimeType))
	if err == nil {
		mediaType = strings.ToLower(mediaType)
		if strings.HasPrefix(mediaType, "text/") ||
			strings.HasSuffix(mediaType, "+json") ||
			strings.HasSuffix(mediaType, "+xml") {
			return true
		}
		switch mediaType {
		case "application/json",
			"application/javascript",
			"application/xml",
			"application/x-sh",
			"application/x-yaml",
			"application/yaml",
			"application/toml":
			return true
		}
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".txt", ".md", ".log", ".json", ".yaml", ".yml", ".toml",
		".env", ".sh", ".bash", ".py", ".go", ".js", ".ts", ".csv", ".xml", ".html":
		return true
	default:
		return false
	}
}
