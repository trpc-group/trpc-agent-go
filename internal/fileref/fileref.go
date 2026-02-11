//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package fileref parses and reads file references like workspace://... and
// artifact://....
//
// Tools use these references to share a unified file view.
package fileref

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcache"
)

const (
	schemeSep = "://"

	// SchemeArtifact is the artifact:// file ref scheme.
	SchemeArtifact = "artifact"
	// SchemeWorkspace is the workspace:// file ref scheme.
	SchemeWorkspace = "workspace"

	// ArtifactPrefix is the "artifact://" prefix.
	ArtifactPrefix = SchemeArtifact + schemeSep
	// WorkspacePrefix is the "workspace://" prefix.
	WorkspacePrefix = SchemeWorkspace + schemeSep
)

const errArtifactNameEmpty = "artifact name is empty"

// Ref is a parsed file reference.
//
// When Scheme is empty, Path is a caller-defined local path
// (for example, relative to a file tool base directory).
type Ref struct {
	Scheme          string
	Path            string
	ArtifactName    string
	ArtifactVersion *int
	Raw             string
}

// WorkspaceRef builds a workspace:// reference for the given relative path.
func WorkspaceRef(rel string) string {
	return WorkspacePrefix + strings.TrimSpace(rel)
}

// Parse parses raw into a Ref.
//
// When the returned Ref has an empty Scheme, the caller should treat Path as
// a local path (for example, relative to a tool base directory).
func Parse(raw string) (Ref, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Ref{Raw: raw}, nil
	}

	if strings.HasPrefix(s, WorkspacePrefix) {
		p := strings.TrimPrefix(s, WorkspacePrefix)
		rel, err := cleanRelPath(p)
		if err != nil {
			return Ref{}, err
		}
		return Ref{
			Scheme: SchemeWorkspace,
			Path:   rel,
			Raw:    raw,
		}, nil
	}

	if strings.HasPrefix(s, ArtifactPrefix) {
		rest := strings.TrimPrefix(s, ArtifactPrefix)
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return Ref{}, fmt.Errorf(errArtifactNameEmpty)
		}
		name, ver, err := parseArtifactRef(rest)
		if err != nil {
			return Ref{}, err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return Ref{}, fmt.Errorf(errArtifactNameEmpty)
		}
		return Ref{
			Scheme:          SchemeArtifact,
			ArtifactName:    name,
			ArtifactVersion: ver,
			Raw:             raw,
		}, nil
	}

	if strings.Contains(s, schemeSep) {
		return Ref{}, fmt.Errorf(
			"unsupported file ref scheme: %s",
			raw,
		)
	}
	return Ref{Path: s, Raw: raw}, nil
}

func cleanRelPath(p string) (string, error) {
	s := strings.TrimSpace(p)
	if s == "" || s == "." {
		return "", nil
	}
	if filepath.IsAbs(s) {
		return "", fmt.Errorf(
			"absolute paths are not allowed: %s",
			p,
		)
	}

	clean := filepath.Clean(s)
	if clean == "." {
		return "", nil
	}
	parent := ".."
	sep := string(os.PathSeparator)
	if clean == parent || strings.HasPrefix(clean, parent+sep) {
		return "", fmt.Errorf(
			"path traversal is not allowed: %s",
			p,
		)
	}
	return clean, nil
}

// TryRead reads raw if it is a supported file reference.
//
// When handled is false, raw is not a reference and the caller should treat
// it as a local path.
func TryRead(
	ctx context.Context,
	raw string,
) (string, string, bool, error) {
	ref, err := Parse(raw)
	if err != nil {
		return "", "", true, err
	}
	switch ref.Scheme {
	case "":
		return "", "", false, nil
	case SchemeWorkspace:
		content, mime, ok := toolcache.LookupSkillRunOutputFileFromContext(
			ctx,
			ref.Path,
		)
		if !ok {
			return "", "", true, fmt.Errorf(
				"workspace file is not exported: %s",
				ref.Path,
			)
		}
		return content, mime, true, nil
	case SchemeArtifact:
		data, mime, _, err := loadArtifactFromContext(
			ctx,
			ref.ArtifactName,
			ref.ArtifactVersion,
		)
		if err != nil {
			return "", "", true, err
		}
		return string(data), mime, true, nil
	default:
		return "", "", true, fmt.Errorf(
			"unsupported file ref scheme: %s",
			ref.Scheme,
		)
	}
}

// WorkspaceFiles returns files exported from skill_run output_files in ctx.
func WorkspaceFiles(
	ctx context.Context,
) []toolcache.SkillRunOutputFile {
	return toolcache.SkillRunOutputFilesFromContext(ctx)
}

func loadArtifactFromContext(
	ctx context.Context,
	name string,
	version *int,
) ([]byte, string, int, error) {
	svc, info, ok := artifactTargetFromContext(ctx)
	if !ok || svc == nil {
		return nil, "", 0, fmt.Errorf("artifact service not in context")
	}
	art, err := svc.LoadArtifact(ctx, info, name, version)
	if err != nil {
		return nil, "", 0, err
	}
	if art == nil {
		return nil, "", 0, fmt.Errorf("artifact not found: %s", name)
	}
	mt := art.MimeType
	if mt == "" {
		mt = "application/octet-stream"
	}
	actual := resolveArtifactVersion(ctx, svc, info, name, version)
	return art.Data, mt, actual, nil
}

func artifactTargetFromContext(
	ctx context.Context,
) (artifact.Service, artifact.SessionInfo, bool) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.ArtifactService == nil ||
		inv.Session == nil {
		return nil, artifact.SessionInfo{}, false
	}
	if inv.Session.AppName == "" || inv.Session.UserID == "" ||
		inv.Session.ID == "" {
		return nil, artifact.SessionInfo{}, false
	}
	info := artifact.SessionInfo{
		AppName:   inv.Session.AppName,
		UserID:    inv.Session.UserID,
		SessionID: inv.Session.ID,
	}
	return inv.ArtifactService, info, true
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

func parseArtifactRef(ref string) (string, *int, error) {
	parts := strings.Split(ref, "@")
	if len(parts) == 1 {
		return parts[0], nil, nil
	}
	if len(parts) == 2 {
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
