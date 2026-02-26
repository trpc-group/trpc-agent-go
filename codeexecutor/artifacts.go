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
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

// LoadArtifactHelper resolves artifact name@version via callback context.
// If version is nil, loads latest. Returns data, mime, actual version.
func LoadArtifactHelper(
	ctx context.Context, name string, version *int,
) ([]byte, string, int, error) {
	svc, ok := ArtifactServiceFromContext(ctx)
	if !ok || svc == nil {
		return nil, "", 0, fmt.Errorf("artifact service not in context")
	}
	info := artifactSessionFromContext(ctx)
	art, err := svc.LoadArtifact(ctx, info, name, version)
	if err != nil {
		return nil, "", 0, err
	}
	if art == nil {
		return nil, "", 0, fmt.Errorf("artifact not found: %s", name)
	}
	actual := resolveArtifactVersion(ctx, svc, info, name, version)
	mt := art.MimeType
	if mt == "" {
		mt = "application/octet-stream"
	}
	return art.Data, mt, actual, nil
}

func resolveArtifactVersion(
	ctx context.Context,
	svc artifact.Service,
	info artifact.SessionInfo,
	name string,
	version *int,
) int {
	if version != nil {
		return *version
	}
	vers, err := svc.ListVersions(ctx, info, name)
	if err != nil || len(vers) == 0 {
		return 0
	}
	max := vers[0]
	for _, v := range vers[1:] {
		if v > max {
			max = v
		}
	}
	return max
}

// ParseArtifactRef splits "name@version" into name and optional version.
func ParseArtifactRef(ref string) (string, *int, error) {
	parts := strings.Split(ref, "@")
	if len(parts) == 1 {
		return parts[0], nil, nil
	}
	if len(parts) == 2 {
		// version may not be strictly numeric across services; keep
		// it simple: try integer, else error.
		var v int
		for _, r := range parts[1] {
			if r < '0' || r > '9' {
				return "", nil, fmt.Errorf("invalid version: %s", parts[1])
			}
		}
		for i := 0; i < len(parts[1]); i++ {
			v = v*10 + int(parts[1][i]-'0')
		}
		return parts[0], &v, nil
	}
	return "", nil, fmt.Errorf("invalid artifact ref: %s", ref)
}

// SaveArtifactHelper saves a file as artifact using callback context.
func SaveArtifactHelper(
	ctx context.Context, filename string, data []byte, mime string,
) (int, error) {
	svc, ok := ArtifactServiceFromContext(ctx)
	if !ok || svc == nil {
		return 0, fmt.Errorf("artifact service not in context")
	}
	info := artifactSessionFromContext(ctx)
	ver, err := svc.SaveArtifact(ctx, info, filename,
		&artifact.Artifact{
			Data:     data,
			MimeType: mime,
			Name:     filename,
		})
	if err != nil {
		return 0, err
	}
	return ver, nil
}

// WithArtifactService attaches artifact.Service to context so lower
// layers (codeexecutor) can resolve artifacts without importing agent.
type artifactKey struct{}
type artifactSessionKey struct{}

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

// WithArtifactSession stores artifact session info in context.
func WithArtifactSession(
	ctx context.Context, info artifact.SessionInfo,
) context.Context {
	return context.WithValue(ctx, artifactSessionKey{}, info)
}

func artifactSessionFromContext(
	ctx context.Context,
) artifact.SessionInfo {
	v := ctx.Value(artifactSessionKey{})
	if v == nil {
		return artifact.SessionInfo{}
	}
	if info, ok := v.(artifact.SessionInfo); ok {
		return info
	}
	return artifact.SessionInfo{}
}
