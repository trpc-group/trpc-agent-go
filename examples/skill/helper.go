//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const (
	stateKeySkillRunOutputFiles = "tool:skill_run:output_files"
)

// skillRunOutputFile represents a file exported from skill_run output_files.
type skillRunOutputFile struct {
	Content  string
	MIMEType string
}

// lookupSkillRunOutputFileFromContext looks up a file from the
// skill_run output_files stored in the invocation state within ctx.
func lookupSkillRunOutputFileFromContext(
	ctx context.Context,
	relPath string,
) (string, string, bool) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return "", "", false
	}

	name := strings.TrimSpace(relPath)
	if name == "" {
		return "", "", false
	}

	v, ok := inv.GetState(stateKeySkillRunOutputFiles)
	if !ok {
		return "", "", false
	}
	m, ok := v.(map[string]skillRunOutputFile)
	if !ok {
		return "", "", false
	}
	f, ok := m[name]
	if !ok {
		return "", "", false
	}
	return f.Content, f.MIMEType, true
}

// skillRunOutputFilesFromContext returns all files exported from
// skill_run output_files in the invocation state within ctx.
func skillRunOutputFilesFromContext(
	ctx context.Context,
) []skillRunOutputFile {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil
	}

	v, ok := inv.GetState(stateKeySkillRunOutputFiles)
	if !ok {
		return nil
	}
	m, ok := v.(map[string]skillRunOutputFile)
	if !ok || len(m) == 0 {
		return nil
	}

	out := make([]skillRunOutputFile, 0, len(m))
	for _, f := range m {
		out = append(out, f)
	}
	return out
}

const (
	schemeSep = "://"

	schemeArtifact  = "artifact"
	schemeWorkspace = "workspace"

	artifactPrefix  = schemeArtifact + schemeSep
	workspacePrefix = schemeWorkspace + schemeSep
)

const errArtifactNameEmpty = "artifact name is empty"

// fileRef is a parsed file reference.
type fileRef struct {
	Scheme          string
	Path            string
	ArtifactName    string
	ArtifactVersion *int
	Raw             string
}

// parseFileRef parses raw into a fileRef.
func parseFileRef(raw string) (fileRef, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return fileRef{Raw: raw}, nil
	}

	if after, ok := strings.CutPrefix(s, workspacePrefix); ok {
		p := after
		rel, err := cleanRelPath(p)
		if err != nil {
			return fileRef{}, err
		}
		return fileRef{
			Scheme: schemeWorkspace,
			Path:   rel,
			Raw:    raw,
		}, nil
	}

	if after, ok := strings.CutPrefix(s, artifactPrefix); ok {
		rest := after
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return fileRef{}, fmt.Errorf(errArtifactNameEmpty)
		}
		name, ver, err := codeexecutor.ParseArtifactRef(rest)
		if err != nil {
			return fileRef{}, err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return fileRef{}, fmt.Errorf(errArtifactNameEmpty)
		}
		return fileRef{
			Scheme:          schemeArtifact,
			ArtifactName:    name,
			ArtifactVersion: ver,
			Raw:             raw,
		}, nil
	}

	if strings.Contains(s, schemeSep) {
		return fileRef{}, fmt.Errorf(
			"unsupported file ref scheme: %s",
			raw,
		)
	}
	return fileRef{Path: s, Raw: raw}, nil
}

func cleanRelPath(p string) (string, error) {
	s := strings.TrimSpace(p)
	if s == "" || s == "." {
		return "", nil
	}
	if filepath.IsAbs(s) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", p)
	}

	clean := filepath.Clean(s)
	if clean == "." {
		return "", nil
	}
	parent := ".."
	sep := string(os.PathSeparator)
	if clean == parent || strings.HasPrefix(clean, parent+sep) {
		return "", fmt.Errorf("path traversal is not allowed: %s", p)
	}
	return clean, nil
}

// tryReadFileRef reads raw if it is a supported file reference.
func tryReadFileRef(
	ctx context.Context,
	raw string,
) (string, string, bool, error) {
	ref, err := parseFileRef(raw)
	if err != nil {
		return "", "", true, err
	}
	switch ref.Scheme {
	case "":
		return "", "", false, nil
	case schemeWorkspace:
		content, mime, ok := lookupSkillRunOutputFileFromContext(
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
	case schemeArtifact:
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
