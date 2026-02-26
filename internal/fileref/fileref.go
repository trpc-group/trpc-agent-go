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
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
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
		name, ver, err := codeexecutor.ParseArtifactRef(rest)
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
	ctxIO := withArtifactContext(ctx)
	return codeexecutor.LoadArtifactHelper(ctxIO, name, version)
}

func withArtifactContext(ctx context.Context) context.Context {
	if svc, ok := codeexecutor.ArtifactServiceFromContext(ctx); ok &&
		svc != nil {
		return ctx
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.ArtifactService == nil ||
		inv.Session == nil {
		return ctx
	}
	info := artifact.SessionInfo{
		AppName:   inv.Session.AppName,
		UserID:    inv.Session.UserID,
		SessionID: inv.Session.ID,
	}
	ctx = codeexecutor.WithArtifactService(ctx, inv.ArtifactService)
	return codeexecutor.WithArtifactSession(ctx, info)
}
