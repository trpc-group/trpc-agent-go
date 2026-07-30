//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

// RedactingArtifactService protects artifact data before it reaches storage.
// Text artifacts are redacted. Binary artifacts containing a recognized
// secret are rejected because rewriting arbitrary binary data is unsafe.
type RedactingArtifactService struct {
	delegate artifact.Service
}

// NewRedactingArtifactService wraps an artifact service with save-time secret
// protection.
func NewRedactingArtifactService(
	delegate artifact.Service,
) artifact.Service {
	return &RedactingArtifactService{delegate: delegate}
}

// SaveArtifact redacts or rejects secret-bearing data before delegating.
func (s *RedactingArtifactService) SaveArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
	value *artifact.Artifact,
) (int, error) {
	delegate, err := s.requireDelegate()
	if err != nil {
		return 0, err
	}
	if value == nil {
		return delegate.SaveArtifact(ctx, sessionInfo, filename, nil)
	}

	redactedData, changed, err := redactArtifactData(
		filename,
		value.MimeType,
		value.Data,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"redact artifact %q: %w",
			filename,
			err,
		)
	}
	if !changed {
		return delegate.SaveArtifact(ctx, sessionInfo, filename, value)
	}
	if !isTextArtifact(filename, value.MimeType) {
		return 0, fmt.Errorf(
			"save artifact %q: artifact contains a recognized secret",
			filename,
		)
	}

	safeCopy := *value
	safeCopy.Data = redactedData
	version, err := delegate.SaveArtifact(
		ctx,
		sessionInfo,
		filename,
		&safeCopy,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"save redacted artifact %q: %w",
			filename,
			err,
		)
	}
	return version, nil
}

// LoadArtifact delegates loading without changing stored data.
func (s *RedactingArtifactService) LoadArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
	version *int,
) (*artifact.Artifact, error) {
	delegate, err := s.requireDelegate()
	if err != nil {
		return nil, err
	}
	return delegate.LoadArtifact(ctx, sessionInfo, filename, version)
}

// ListArtifactKeys delegates artifact key listing.
func (s *RedactingArtifactService) ListArtifactKeys(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
) ([]string, error) {
	delegate, err := s.requireDelegate()
	if err != nil {
		return nil, err
	}
	return delegate.ListArtifactKeys(ctx, sessionInfo)
}

// DeleteArtifact delegates artifact deletion.
func (s *RedactingArtifactService) DeleteArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
) error {
	delegate, err := s.requireDelegate()
	if err != nil {
		return err
	}
	return delegate.DeleteArtifact(ctx, sessionInfo, filename)
}

// ListVersions delegates artifact version listing.
func (s *RedactingArtifactService) ListVersions(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
) ([]int, error) {
	delegate, err := s.requireDelegate()
	if err != nil {
		return nil, err
	}
	return delegate.ListVersions(ctx, sessionInfo, filename)
}

func (s *RedactingArtifactService) requireDelegate() (
	artifact.Service,
	error,
) {
	if s == nil || s.delegate == nil {
		return nil, fmt.Errorf(
			"redacting artifact service: delegate is required",
		)
	}
	return s.delegate, nil
}

func redactArtifactData(
	filename string,
	mimeType string,
	data []byte,
) ([]byte, bool, error) {
	if isJSONArtifact(filename, mimeType) {
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, false, fmt.Errorf(
				"parse JSON artifact: %w",
				err,
			)
		}
		redacted, changed := redactJSONValue(value)
		if !changed {
			return data, false, nil
		}
		encoded, err := json.Marshal(redacted)
		if err != nil {
			return nil, false, fmt.Errorf(
				"marshal redacted JSON: %w",
				err,
			)
		}
		return encoded, true, nil
	}

	redacted, changed := redactString(string(data))
	if !changed {
		return data, false, nil
	}
	return []byte(redacted), true, nil
}

func isJSONArtifact(filename string, mimeType string) bool {
	mediaType, _, err := mime.ParseMediaType(
		strings.TrimSpace(mimeType),
	)
	if err == nil {
		mediaType = strings.ToLower(mediaType)
		if mediaType == "application/json" ||
			strings.HasSuffix(mediaType, "+json") {
			return true
		}
	}
	return strings.EqualFold(filepath.Ext(filename), ".json")
}

func isTextArtifact(filename string, mimeType string) bool {
	mediaType, _, err := mime.ParseMediaType(
		strings.TrimSpace(mimeType),
	)
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
			"application/toml",
			"application/xml",
			"application/x-httpd-php",
			"application/x-sh",
			"application/x-yaml",
			"application/yaml":
			return true
		}
	}

	switch strings.ToLower(filepath.Ext(filename)) {
	case ".bash",
		".conf",
		".css",
		".csv",
		".env",
		".go",
		".html",
		".ini",
		".js",
		".json",
		".log",
		".md",
		".py",
		".sh",
		".text",
		".toml",
		".ts",
		".tsv",
		".txt",
		".xml",
		".yaml",
		".yml":
		return true
	default:
		return false
	}
}
