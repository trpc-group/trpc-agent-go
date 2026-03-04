//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeexecutor

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

// LoadArtifactHelper resolves artifact name@version via callback context.
// If version is nil, loads latest. Returns data, mime, actual version.
func LoadArtifactHelper(
	ctx context.Context, name string, version *artifact.VersionID,
) ([]byte, string, artifact.VersionID, error) {
	svc, ok := ArtifactServiceFromContext(ctx)
	if !ok || svc == nil {
		return nil, "", "", fmt.Errorf("artifact service not in context")
	}
	baseKey := artifactBaseKeyFromContext(ctx)
	data, desc, err := artifact.ReadAll(ctx, svc, withName(baseKey, name), version)
	if err != nil {
		return nil, "", "", err
	}
	actual := desc.Version
	mt := desc.MimeType
	if mt == "" {
		mt = "application/octet-stream"
	}
	return data, mt, actual, nil
}

// ParseArtifactRef splits "name@version" into name and optional version.
func ParseArtifactRef(ref string) (string, *artifact.VersionID, error) {
	parts := strings.Split(ref, "@")
	if len(parts) == 1 {
		return parts[0], nil, nil
	}
	if len(parts) == 2 {
		v := artifact.VersionID(parts[1])
		if strings.TrimSpace(string(v)) == "" {
			return "", nil, fmt.Errorf("invalid version: %s", parts[1])
		}
		return parts[0], &v, nil
	}
	return "", nil, fmt.Errorf("invalid artifact ref: %s", ref)
}

// SaveArtifactHelper saves a file as artifact using callback context.
func SaveArtifactHelper(
	ctx context.Context, filename string, data []byte, mime string,
) (artifact.VersionID, error) {
	svc, ok := ArtifactServiceFromContext(ctx)
	if !ok || svc == nil {
		return "", fmt.Errorf("artifact service not in context")
	}
	baseKey := artifactBaseKeyFromContext(ctx)
	desc, err := svc.Put(ctx, withName(baseKey, filename), bytes.NewReader(data), artifact.WithPutMimeType(mime))
	if err != nil {
		return "", err
	}
	return desc.Version, nil
}

// WithArtifactService attaches artifact.Service to context so lower
// layers (codeexecutor) can resolve artifacts without importing agent.
type artifactKey struct{}
type artifactBaseKey struct{}

// WithArtifactService stores an artifact.Service in the context.
// Callers retrieve it in lower layers to load/save artifacts
// without importing higher-level packages.
func WithArtifactService(
	ctx context.Context, svc artifact.Service,
) context.Context {
	return context.WithValue(ctx, artifactKey{}, svc)
}

// ArtifactServiceFromContext fetches the artifact.Service previously
// stored by WithArtifactService. It returns the service and a boolean
// indicating presence.
func ArtifactServiceFromContext(
	ctx context.Context,
) (artifact.Service, bool) {
	v := ctx.Value(artifactKey{})
	if v == nil {
		return nil, false
	}
	svc, ok := v.(artifact.Service)
	return svc, ok
}

// WithArtifactBaseKey stores the base artifact key (without Name) in context.
func WithArtifactBaseKey(ctx context.Context, key artifact.Key) context.Context {
	key.Name = ""
	return context.WithValue(ctx, artifactBaseKey{}, key)
}

func artifactBaseKeyFromContext(ctx context.Context) artifact.Key {
	v := ctx.Value(artifactBaseKey{})
	if v == nil {
		return artifact.Key{}
	}
	if k, ok := v.(artifact.Key); ok {
		return k
	}
	return artifact.Key{}
}

func withName(base artifact.Key, name string) artifact.Key {
	base.Name = name
	return base
}
